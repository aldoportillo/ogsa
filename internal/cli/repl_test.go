package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldoportillo/ogsa/internal/agent"
	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

type fakeProvider struct {
	baseURL string
	models  []string
	seen    *[]llm.Request
	support llm.ToolSupport
	checks  *int
}

func (f fakeProvider) ListModels(context.Context) ([]string, error) { return f.models, nil }

func (f fakeProvider) CheckTools(context.Context, string) (llm.ToolSupport, error) {
	*f.checks++
	return f.support, nil
}

func (f fakeProvider) Chat(_ context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	*f.seen = append(*f.seen, req)
	reply := "echo from " + f.baseURL
	onDelta(reply)
	return llm.Message{Role: llm.RoleAssistant, Content: reply}, nil
}

func run(t *testing.T, input string) (*REPL, string, []llm.Request) {
	t.Helper()
	r, out, seen, _ := runWith(t, input, llm.ToolsSupported)
	return r, out, seen
}

func runWith(t *testing.T, input string, support llm.ToolSupport) (*REPL, string, []llm.Request, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var seen []llm.Request
	var checks int
	r := New(cfg, strings.NewReader(input), &out)
	r.NewProvider = func(p config.Provider) llm.Provider {
		return fakeProvider{baseURL: p.BaseURL, models: []string{"small", "big"}, seen: &seen, support: support, checks: &checks}
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Config = saved
	return r, out.String(), seen, checks
}

func TestModelPickFromList(t *testing.T) {
	r, out, _ := run(t, "/model\n\n2\n/exit\n")
	if r.Config.Provider != "ollama" || r.Config.Model != "big" {
		t.Fatalf("config = %s/%s\n%s", r.Config.Provider, r.Config.Model, out)
	}
}

func TestModelAddProviderThenChat(t *testing.T) {
	input := "/model\na\nhome\nhttps://llm.home/v1/\n$HOME_KEY\n1\nhello\nagain\n"
	r, out, seen := run(t, input)

	p, ok := r.Config.Find("home")
	if !ok || p.BaseURL != "https://llm.home/v1" || p.APIKeyEnv != "HOME_KEY" {
		t.Fatalf("provider = %+v, %v", p, ok)
	}
	if r.Config.Provider != "home" || r.Config.Model != "small" {
		t.Fatalf("config = %s/%s", r.Config.Provider, r.Config.Model)
	}
	if !strings.Contains(out, "echo from https://llm.home/v1") {
		t.Fatalf("chat did not use new provider:\n%s", out)
	}
	if len(seen) != 2 || len(seen[1].Messages) != 4 || seen[1].Model != "small" {
		t.Fatalf("history not carried: %+v", seen)
	}
}

func TestModelDirectAndClear(t *testing.T) {
	r, out, seen := run(t, "/model big\nhi\n/clear\nhi\n/quit\n")
	if r.Config.Model != "big" {
		t.Fatalf("model = %q", r.Config.Model)
	}
	if len(seen) != 2 || len(seen[1].Messages) != 2 {
		t.Fatalf("clear did not reset history: %+v\n%s", seen, out)
	}
}

func TestChatWithoutModel(t *testing.T) {
	_, out, seen := run(t, "hi\n")
	if len(seen) != 0 || !strings.Contains(out, "no model selected") {
		t.Fatalf("expected guard, got:\n%s", out)
	}
}

type noopTool struct{}

func (noopTool) Spec() llm.ToolSpec { return llm.ToolSpec{Name: "noop"} }

func (noopTool) Run(context.Context, json.RawMessage) (string, error) { return "", nil }

func TestModelCheckRecordsSupportOnce(t *testing.T) {
	// Picking the same model twice checks once; /model check forces another.
	r, out, _, checks := runWith(t, "/model\n\n1\n/model\n\n1\n/model check\n", llm.ToolsSupported)
	if checks != 2 {
		t.Fatalf("checks = %d, want 2\n%s", checks, out)
	}
	if got := r.Config.ModelInfo("ollama", "small").Tools; got != llm.ToolsSupported {
		t.Fatalf("saved tools = %q", got)
	}
	if !strings.Contains(out, "tools: supported") {
		t.Fatalf("status line missing tools:\n%s", out)
	}
}

func TestUnsupportedModelChatsWithoutTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := config.Load(path)
	var out strings.Builder
	var seen []llm.Request
	var checks int
	r := New(cfg, strings.NewReader("/model tiny\nhi\n"), &out)
	r.Tools = []agent.Tool{noopTool{}}
	r.NewProvider = func(p config.Provider) llm.Provider {
		return fakeProvider{baseURL: p.BaseURL, seen: &seen, support: llm.ToolsUnsupported, checks: &checks}
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plain chat only") {
		t.Fatalf("no warning:\n%s", out.String())
	}
	if len(seen) != 1 || len(seen[0].Tools) != 0 {
		t.Fatalf("tools sent to unsupported model: %+v", seen)
	}
}

func TestSystemPromptLeadsEachRequest(t *testing.T) {
	_, _, seen := run(t, "/model small\nhi\n")
	if len(seen) != 1 {
		t.Fatalf("seen = %+v", seen)
	}
	first := seen[0].Messages[0]
	if first.Role != llm.RoleSystem || !strings.Contains(first.Content, "no tools") {
		t.Fatalf("first message = %+v", first)
	}
}
