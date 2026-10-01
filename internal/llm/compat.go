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
	"slices"
	"strings"
	"time"
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
	return doHTTP(c.HTTP, req)
}

func doHTTP(hc *http.Client, req *http.Request) (*http.Response, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &HTTPError{Method: req.Method, URL: req.URL.String(), StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(msg))}
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

type wireFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type wireToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function wireFunction `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toWireMessage(m Message) wireMessage {
	w := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		args := tc.Arguments
		if args == "" {
			args = "{}"
		}
		w.ToolCalls = append(w.ToolCalls, wireToolCall{
			ID:       tc.ID,
			Type:     "function",
			Function: wireFunction{Name: tc.Name, Arguments: args},
		})
	}
	return w
}

func toWireTool(s ToolSpec) wireTool {
	var t wireTool
	t.Type = "function"
	t.Function.Name = s.Name
	t.Function.Description = s.Description
	t.Function.Parameters = s.Parameters
	if len(t.Function.Parameters) == 0 {
		t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t
}

func mergeToolCallDelta(calls []ToolCall, d wireToolCall) []ToolCall {
	i := len(calls)
	switch {
	case d.Index != nil:
		i = *d.Index
	case d.ID == "" && d.Function.Name == "" && len(calls) > 0:
		i = len(calls) - 1 // unindexed continuation of the previous call
	}
	for len(calls) <= i {
		calls = append(calls, ToolCall{})
	}
	if d.ID != "" {
		calls[i].ID = d.ID
	}
	if d.Function.Name != "" {
		calls[i].Name = d.Function.Name
	}
	calls[i].Arguments += d.Function.Arguments
	return calls
}

func (c *CompatClient) Chat(ctx context.Context, r Request, onDelta func(string)) (Message, error) {
	msgs := make([]wireMessage, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = toWireMessage(m)
	}
	body := map[string]any{
		"model":    r.Model,
		"messages": msgs,
		"stream":   true,
	}

	if len(r.Tools) > 0 {
		tools := make([]wireTool, len(r.Tools))
		for i, s := range r.Tools {
			tools[i] = toWireTool(s)
		}
		body["tools"] = tools
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", body)
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
	var calls []ToolCall
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
			for _, d := range ch.Delta.ToolCalls {
				calls = mergeToolCallDelta(calls, d)
			}
		}
	}
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", i)
		}
	}
	msg := Message{Role: RoleAssistant, Content: out.String(), ToolCalls: calls}
	if err := sc.Err(); err != nil {
		return msg, err
	}
	return msg, nil
}

func (c *CompatClient) CheckTools(ctx context.Context, model string) (ToolSupport, error) {
	for _, lookup := range []func(context.Context, string) (ToolSupport, bool){c.ollamaTools, c.litellmTools} {
		mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s, ok := lookup(mctx, model)
		cancel()
		if ok {
			return s, nil
		}
	}
	return ProbeTools(ctx, c, model)
}

func (c *CompatClient) ollamaTools(ctx context.Context, model string) (ToolSupport, bool) {
	native := &CompatClient{BaseURL: strings.TrimSuffix(c.BaseURL, "/v1"), APIKey: c.APIKey, HTTP: c.HTTP}
	req, err := native.newRequest(ctx, http.MethodPost, "/api/show", map[string]string{"model": model})
	if err != nil {
		return ToolsUnknown, false
	}
	resp, err := c.do(req)
	if err != nil {
		return ToolsUnknown, false
	}
	defer resp.Body.Close()
	var body struct {
		Capabilities *[]string `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || body.Capabilities == nil {
		return ToolsUnknown, false
	}
	if slices.Contains(*body.Capabilities, "tools") {
		return ToolsSupported, true
	}
	return ToolsUnsupported, true
}

func (c *CompatClient) litellmTools(ctx context.Context, model string) (ToolSupport, bool) {
	req, err := c.newRequest(ctx, http.MethodGet, "/model/info", nil)
	if err != nil {
		return ToolsUnknown, false
	}
	resp, err := c.do(req)
	if err != nil {
		return ToolsUnknown, false
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				SupportsFunctionCalling *bool `json:"supports_function_calling"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return ToolsUnknown, false
	}
	for _, m := range body.Data {
		if m.ModelName == model && m.ModelInfo.SupportsFunctionCalling != nil {
			if *m.ModelInfo.SupportsFunctionCalling {
				return ToolsSupported, true
			}
			return ToolsUnsupported, true
		}
	}
	return ToolsUnknown, false
}
