package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aldoportillo/ogsa/internal/agent"
	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
	"github.com/aldoportillo/ogsa/internal/session"
)

type replyScript struct {
	replies []llm.Message
	seen    *[]llm.Request
}

func (s *replyScript) ListModels(context.Context) ([]string, error) { return []string{"m"}, nil }
func (s *replyScript) CheckTools(context.Context, string) (llm.ToolSupport, error) {
	return llm.ToolsSupported, nil
}
func (s *replyScript) Chat(_ context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	*s.seen = append(*s.seen, req)
	if len(s.replies) == 0 {
		return llm.Message{}, errors.New("server went away")
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	if onDelta != nil {
		onDelta(r.Content)
	}
	return r, nil
}

type project struct {
	cfgPath, workDir string
	store            *session.Store
}

func newProject(t *testing.T) project {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config", "config.json")
	cfg, _ := config.Load(cfgPath)
	cfg.Model = "m"
	cfg.SetModelInfo("ollama", "m", config.ModelInfo{Tools: llm.ToolsSupported})
	cfg.Save()
	work := filepath.Join(dir, "work")
	os.MkdirAll(work, 0o755)
	return project{cfgPath, work, session.ForProject(filepath.Dir(cfgPath), work)}
}

func (p project) run(t *testing.T, input string, replies []llm.Message, cont bool) (string, []llm.Request) {
	t.Helper()
	cfg, _ := config.Load(p.cfgPath)
	var out strings.Builder
	var seen []llm.Request
	r := New(cfg, strings.NewReader(input), &out)
	r.WorkDir, r.Sessions = p.workDir, p.store
	r.Tools = []agent.Tool{noopTool{}}
	r.NewProvider = func(config.Provider) llm.Provider {
		if replies == nil {
			var checks int
			return fakeProvider{baseURL: "echo", seen: &seen, support: llm.ToolsSupported, checks: &checks}
		}
		return &replyScript{replies: replies, seen: &seen}
	}
	if cont {
		if err := r.ContinueLast(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	return out.String(), seen
}

func roles(msgs []llm.Message) string {
	var rs []string
	for _, m := range msgs {
		rs = append(rs, string(m.Role))
	}
	return strings.Join(rs, ",")
}

func TestContinueLastSession(t *testing.T) {
	p := newProject(t)
	p.run(t, "first question\n", nil, false)

	out, seen := p.run(t, "follow-up\n", nil, true)
	if !strings.Contains(out, `resumed session from just now (2 messages): "first question"`) {
		t.Fatalf("output:\n%s", out)
	}
	if got := roles(seen[0].Messages); got != "system,user,assistant,user" {
		t.Fatalf("request roles = %s", got)
	}
	infos, _ := p.store.List()
	if len(infos) != 1 || infos[0].Messages != 4 {
		t.Fatalf("continued session should append to the same file: %+v", infos)
	}
}

func TestStartupHintAndResumeCommand(t *testing.T) {
	p := newProject(t)
	p.run(t, "old topic\n", nil, false)
	infos, _ := p.store.List()
	past := time.Now().Add(-3 * time.Hour)
	os.Chtimes(infos[0].Path, past, past)
	p.run(t, "new topic\n", nil, false)

	out, seen := p.run(t, "/resume\n2\nmore\n", nil, false)
	if !strings.Contains(out, `last session: just now, "new topic"`) {
		t.Errorf("no startup hint:\n%s", out)
	}
	if !strings.Contains(out, "old topic") || len(seen) != 1 || seen[0].Messages[1].Content != "old topic" {
		t.Fatalf("resumed the wrong session:\n%s", out)
	}
}

func TestClearStartsNewSession(t *testing.T) {
	p := newProject(t)
	out, seen := p.run(t, "one\n/clear\ntwo\n", nil, false)
	if !strings.Contains(out, "/resume brings back the previous one") {
		t.Errorf("output:\n%s", out)
	}
	if got := roles(seen[1].Messages); got != "system,user" {
		t.Fatalf("history not cleared: %s", got)
	}
	if infos, _ := p.store.List(); len(infos) != 2 {
		t.Fatalf("sessions = %d, want 2", len(infos))
	}
}

func TestFailedTurnKeepsCompletedWork(t *testing.T) {
	p := newProject(t)
	replies := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: "{}"}}},
	}
	out, _ := p.run(t, "do the thing\n", replies, false)
	if !strings.Contains(out, "server went away") {
		t.Fatalf("output:\n%s", out)
	}
	infos, _ := p.store.List()
	msgs, _ := session.Load(infos[0].Path)
	if got := roles(msgs); got != "user,assistant,tool" {
		t.Fatalf("saved roles = %s", got)
	}

	_, seen := p.run(t, "try again\n", nil, true)
	if got := roles(seen[0].Messages); got != "system,user,assistant,tool,user" {
		t.Fatalf("retry roles = %s", got)
	}
}

func TestProjectInstructionsReachTheModel(t *testing.T) {
	p := newProject(t)
	os.MkdirAll(filepath.Join(p.workDir, ".ogsa"), 0o755)
	os.WriteFile(filepath.Join(p.workDir, ".ogsa", "OGSA.md"), []byte("Model code is in internal/llm."), 0o644)
	_, seen := p.run(t, "where is the model code?\n", nil, false)
	if !strings.Contains(seen[0].Messages[0].Content, "Model code is in internal/llm.") {
		t.Fatalf("system prompt:\n%s", seen[0].Messages[0].Content)
	}
}

func TestInit(t *testing.T) {
	p := newProject(t)
	out, seen := p.run(t, "/init\n", nil, false)
	if len(seen) != 1 || !strings.HasPrefix(seen[0].Messages[1].Content, "Write .ogsa/OGSA.md") {
		t.Fatalf("init prompt not sent:\n%s", out)
	}

	os.MkdirAll(filepath.Join(p.workDir, ".ogsa"), 0o755)
	os.WriteFile(filepath.Join(p.workDir, ".ogsa", "OGSA.md"), []byte("old"), 0o644)
	if _, seen := p.run(t, "/init\nn\n", nil, false); len(seen) != 0 {
		t.Fatal("sent a request after declining")
	}
	if _, seen := p.run(t, "/init\ny\n", nil, false); len(seen) != 1 || !strings.HasPrefix(seen[0].Messages[len(seen[0].Messages)-1].Content, "Update .ogsa/OGSA.md") {
		t.Fatal("update prompt not sent")
	}
}
