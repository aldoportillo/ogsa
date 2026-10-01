package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToAnthropic(t *testing.T) {
	system, msgs := toAnthropic([]Message{
		{Role: RoleSystem, Content: "be brief"},
		{Role: RoleUser, Content: "find todos"},
		{Role: RoleAssistant, Content: "looking", ToolCalls: []ToolCall{
			{ID: "t1", Name: "grep", Arguments: `{"pattern":"TODO"}`},
			{ID: "t2", Name: "glob", Arguments: ""},
		}},
		{Role: RoleTool, ToolCallID: "t1", Content: "a.go:1:TODO"},
		{Role: RoleTool, ToolCallID: "t2", Content: "a.go"},
		{Role: RoleUser, Content: "thanks"},
	})
	if system != "be brief" {
		t.Fatalf("system = %q", system)
	}
	got, _ := json.Marshal(msgs)
	want := `[` +
		`{"role":"user","content":[{"type":"text","text":"find todos"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"looking"},` +
		`{"type":"tool_use","id":"t1","name":"grep","input":{"pattern":"TODO"}},` +
		`{"type":"tool_use","id":"t2","name":"glob","input":{}}]},` +
		// Tool results and the next user message merge into one user turn.
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go:1:TODO"},` +
		`{"type":"tool_result","tool_use_id":"t2","content":"a.go"},` +
		`{"type":"text","text":"thanks"}]}]`
	if string(got) != want {
		t.Fatalf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestAnthropicChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" {
			t.Errorf("headers = %v", r.Header)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["system"] != "sys" || body["max_tokens"] == nil || body["stream"] != true {
			t.Errorf("body = %v", body)
		}
		tool := body["tools"].([]any)[0].(map[string]any)
		if tool["name"] != "read" || tool["input_schema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool = %v", tool)
		}
		for _, ev := range []string{
			`{"type":"message_start","message":{}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"look."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"read","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		} {
			var typ struct{ Type string }
			json.Unmarshal([]byte(ev), &typ)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ.Type, ev)
		}
	}))
	defer srv.Close()

	var deltas []string
	msg, err := NewAnthropicClient(srv.URL+"/anthropic/v1/messages", "k").Chat(context.Background(), Request{
		Model:    "claude",
		Messages: []Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hi"}},
		Tools:    []ToolSpec{{Name: "read"}},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "Let me look." || strings.Join(deltas, "|") != "Let me |look." {
		t.Fatalf("text = %q deltas = %v", msg.Content, deltas)
	}
	want := []ToolCall{{ID: "tu_1", Name: "read", Arguments: `{"path":"a.go"}`}}
	if fmt.Sprint(msg.ToolCalls) != fmt.Sprint(want) {
		t.Fatalf("tool calls = %+v", msg.ToolCalls)
	}
}

func TestAnthropicStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer srv.Close()
	_, err := NewAnthropicClient(srv.URL, "").Chat(context.Background(), Request{Model: "m"}, nil)
	if err == nil || err.Error() != "Overloaded" {
		t.Fatalf("err = %v", err)
	}
}

func TestAnthropicListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/nolist") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-a"},{"type":"model","id":"claude-b"}],"has_more":false}`)
	}))
	defer srv.Close()

	got, err := NewAnthropicClient(srv.URL, "").ListModels(context.Background())
	if err != nil || strings.Join(got, ",") != "claude-a,claude-b" {
		t.Fatalf("models = %v, %v", got, err)
	}
	if _, err := NewAnthropicClient(srv.URL+"/nolist", "").ListModels(context.Background()); !errors.Is(err, ErrNoModelList) {
		t.Fatalf("err = %v, want ErrNoModelList", err)
	}
}

func TestAnthropicRoot(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.anthropic.com":                             "https://api.anthropic.com",
		"https://api.anthropic.com/v1/":                         "https://api.anthropic.com",
		"https://x.services.ai.azure.com/anthropic/v1/messages": "https://x.services.ai.azure.com/anthropic",
		"https://x.services.ai.azure.com/anthropic":             "https://x.services.ai.azure.com/anthropic",
	} {
		if got := anthropicRoot(in); got != want {
			t.Errorf("anthropicRoot(%q) = %q, want %q", in, got, want)
		}
	}
}
