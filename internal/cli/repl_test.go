package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

type fakeProvider struct {
	baseURL string
	models  []string
	seen    *[]llm.Request
}

func (f fakeProvider) ListModels(context.Context) ([]string, error) { return f.models, nil }

func (f fakeProvider) Chat(_ context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	*f.seen = append(*f.seen, req)
	reply := "echo from " + f.baseURL
	onDelta(reply)
	return llm.Message{Role: llm.RoleAssistant, Content: reply}, nil
}

func run(t *testing.T, input string) (*REPL, string, []llm.Request) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var seen []llm.Request
	r := New(cfg, strings.NewReader(input), &out)
	r.NewProvider = func(p config.Provider) llm.Provider {
		return fakeProvider{baseURL: p.BaseURL, models: []string{"small", "big"}, seen: &seen}
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Config = saved
	return r, out.String(), seen
}

func TestModelPickFromList(t *testing.T) {
	r, out, _ := run(t, "/model\n\n2\n/exit\n")
	if r.Config.Provider != "ollama" || r.Config.Model != "big" {
		t.Fatalf("config = %s/%s\n%s", r.Config.Provider, r.Config.Model, out)
	}
}

func TestModelAddProviderThenChat(t *testing.T) {
	input := "/model\na\nhome\nhttps://llm.home/v1/\nHOME_KEY\n1\nhello\nagain\n"
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
	if len(seen) != 2 || len(seen[1].Messages) != 3 || seen[1].Model != "small" {
		t.Fatalf("history not carried: %+v", seen)
	}
}

func TestModelDirectAndClear(t *testing.T) {
	r, out, seen := run(t, "/model big\nhi\n/clear\nhi\n/quit\n")
	if r.Config.Model != "big" {
		t.Fatalf("model = %q", r.Config.Model)
	}
	if len(seen) != 2 || len(seen[1].Messages) != 1 {
		t.Fatalf("clear did not reset history: %+v\n%s", seen, out)
	}
}

func TestChatWithoutModel(t *testing.T) {
	_, out, seen := run(t, "hi\n")
	if len(seen) != 0 || !strings.Contains(out, "no model selected") {
		t.Fatalf("expected guard, got:\n%s", out)
	}
}
