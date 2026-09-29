package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type CompatClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewCompatClient(baseURL, apiKey string) *CompatClient {
	return &CompatClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    http.DefaultClient,
	}
}

func (c *CompatClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	return req, nil
}

func (c *CompatClient) do(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%s %s: %s: %s", req.Method, req.URL, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (c *CompatClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding model list: %w", err)
	}
	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *CompatClient) Chat(ctx context.Context, r Request, onDelta func(string)) (Message, error) {
	msgs := make([]wireMessage, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = wireMessage{Role: string(m.Role), Content: m.Content}
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", map[string]any{
		"model":    r.Model,
		"messages": msgs,
		"stream":   true,
	})
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var out strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Message{}, fmt.Errorf("decoding stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return Message{}, errors.New(chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if d := ch.Delta.Content; d != "" {
				out.WriteString(d)
				if onDelta != nil {
					onDelta(d)
				}
			}
		}
	}
	msg := Message{Role: RoleAssistant, Content: out.String()}
	if err := sc.Err(); err != nil {
		return msg, err
	}
	return msg, nil
}
