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

type AnthropicClient struct {
	BaseURL   string
	APIKey    string
	MaxTokens int
	HTTP      *http.Client
}

const (
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 8192
)

var ErrNoModelList = errors.New("this server doesn't list its models")

func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	return &AnthropicClient{BaseURL: anthropicRoot(baseURL), APIKey: apiKey, HTTP: http.DefaultClient}
}

func anthropicRoot(u string) string {
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/messages")
	u = strings.TrimSuffix(u, "/models")
	return strings.TrimSuffix(u, "/v1")
}

func (c *AnthropicClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
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
	req.Header.Set("anthropic-version", anthropicVersion)
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	return req, nil
}

func (c *AnthropicClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	resp, err := doHTTP(c.HTTP, req)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return nil, ErrNoModelList
	}
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

func (c *AnthropicClient) CheckTools(context.Context, string) (ToolSupport, error) {
	return ToolsSupported, nil
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func toAnthropic(msgs []Message) (system string, out []anthropicMessage) {
	var sys []string
	add := func(role string, blocks ...anthropicBlock) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			sys = append(sys, m.Content)
		case RoleUser:
			add("user", anthropicBlock{Type: "text", Text: m.Content})
		case RoleTool:
			add("user", anthropicBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content})
		case RoleAssistant:
			var blocks []anthropicBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if !json.Valid(input) {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			add("assistant", blocks...)
		}
	}
	return strings.Join(sys, "\n\n"), out
}

type anthropicEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *AnthropicClient) Chat(ctx context.Context, r Request, onDelta func(string)) (Message, error) {
	system, msgs := toAnthropic(r.Messages)
	maxTokens := c.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	body := map[string]any{
		"model":      r.Model,
		"max_tokens": maxTokens,
		"messages":   msgs,
		"stream":     true,
	}
	if system != "" {
		body["system"] = system
	}
	if len(r.Tools) > 0 {
		tools := make([]anthropicTool, len(r.Tools))
		for i, s := range r.Tools {
			schema := s.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools[i] = anthropicTool{Name: s.Name, Description: s.Description, InputSchema: schema}
		}
		body["tools"] = tools
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/messages", body)
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := doHTTP(c.HTTP, req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var text strings.Builder
	var calls []ToolCall
	toolAt := map[int]int{} // content block index → position in calls
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue // event: lines and blank separators
		}
		var ev anthropicEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			return Message{}, fmt.Errorf("decoding stream event: %w", err)
		}
		switch ev.Type {
		case "error":
			if ev.Error != nil {
				return Message{}, errors.New(ev.Error.Message)
			}
			return Message{}, errors.New("the server reported an error mid-stream")
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				toolAt[ev.Index] = len(calls)
				calls = append(calls, ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name})
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			case "input_json_delta":
				if i, ok := toolAt[ev.Index]; ok {
					calls[i].Arguments += ev.Delta.PartialJSON
				}
			}
		case "message_stop":
			return finishAnthropic(text.String(), calls), nil
		}
	}
	if err := sc.Err(); err != nil {
		return finishAnthropic(text.String(), calls), err
	}
	return finishAnthropic(text.String(), calls), nil
}

func finishAnthropic(text string, calls []ToolCall) Message {
	for i := range calls {
		if calls[i].Arguments == "" {
			calls[i].Arguments = "{}" // tools without parameters stream no input
		}
	}
	return Message{Role: RoleAssistant, Content: text, ToolCalls: calls}
}
