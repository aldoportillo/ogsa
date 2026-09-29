package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aldoportillo/ogsa/internal/llm"
)

type scripted struct {
	replies []llm.Message
	reqs    []llm.Request
}

func (s *scripted) ListModels(context.Context) ([]string, error) { return nil, nil }

func (s *scripted) Chat(_ context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	s.reqs = append(s.reqs, req)
	if len(s.replies) == 0 {
		return llm.Message{}, errors.New("script exhausted")
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	if onDelta != nil && r.Content != "" {
		onDelta(r.Content)
	}
	return r, nil
}

type echoTool struct{}

func (echoTool) Spec() llm.ToolSpec { return llm.ToolSpec{Name: "echo"} }

func (echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct{ Text string }
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.Text == "" {
		return "", errors.New("text is required")
	}
	return "echo: " + a.Text, nil
}

func call(id, name, args string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}}
}

func TestRunToolRoundTrip(t *testing.T) {
	p := &scripted{replies: []llm.Message{
		call("c1", "echo", `{"text":"hi"}`),
		{Role: llm.RoleAssistant, Content: "done"},
	}}
	history := []llm.Message{{Role: llm.RoleUser, Content: "go"}}

	var events []string
	turn, err := Run(context.Background(), p, "m", []Tool{echoTool{}}, history, Hooks{
		OnToolCall:   func(c llm.ToolCall) { events = append(events, "call "+c.Name) },
		OnToolResult: func(_ llm.ToolCall, out string, _ error) { events = append(events, "result "+out) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(turn) != 3 || turn[1].Role != llm.RoleTool || turn[1].ToolCallID != "c1" ||
		turn[1].Content != "echo: hi" || turn[2].Content != "done" {
		t.Fatalf("turn = %+v", turn)
	}
	if strings.Join(events, "|") != "call echo|result echo: hi" {
		t.Fatalf("events = %v", events)
	}
	if len(p.reqs) != 2 || len(p.reqs[0].Tools) != 1 || p.reqs[0].Tools[0].Name != "echo" {
		t.Fatalf("tools not advertised: %+v", p.reqs)
	}
	if len(p.reqs[1].Messages) != 3 {
		t.Fatalf("second request should carry call + result, got %+v", p.reqs[1].Messages)
	}
	if len(history) != 1 {
		t.Fatalf("caller's history was modified: %+v", history)
	}
}

func TestRunReportsToolErrorsToModel(t *testing.T) {
	p := &scripted{replies: []llm.Message{
		call("c1", "nope", `{}`),
		call("c2", "echo", `{}`),
		{Role: llm.RoleAssistant, Content: "gave up"},
	}}
	turn, err := Run(context.Background(), p, "m", []Tool{echoTool{}}, nil, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if got := turn[1].Content; got != `error: unknown tool "nope"` {
		t.Fatalf("unknown tool result = %q", got)
	}
	if got := turn[3].Content; got != "error: text is required" {
		t.Fatalf("tool error result = %q", got)
	}
}

func TestRunStopsAtMaxSteps(t *testing.T) {
	p := &scripted{}
	for range MaxSteps {
		p.replies = append(p.replies, call("c", "echo", `{"text":"again"}`))
	}
	_, err := Run(context.Background(), p, "m", []Tool{echoTool{}}, nil, Hooks{})
	if err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWithoutToolsIsPlainChat(t *testing.T) {
	p := &scripted{replies: []llm.Message{{Role: llm.RoleAssistant, Content: "hello"}}}
	var text string
	turn, err := Run(context.Background(), p, "m", nil, nil, Hooks{OnText: func(d string) { text += d }})
	if err != nil || len(turn) != 1 || text != "hello" || len(p.reqs[0].Tools) != 0 {
		t.Fatalf("turn=%+v text=%q err=%v", turn, text, err)
	}
}
