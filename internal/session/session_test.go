package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aldoportillo/ogsa/internal/llm"
)

func TestAppendLoadRoundTrip(t *testing.T) {
	st := ForProject(t.TempDir(), "/Users/me/code/my app")
	s := st.New()
	if s.Path() != "" {
		t.Fatal("file created before any message")
	}
	turn := []llm.Message{
		{Role: llm.RoleUser, Content: "find todos"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "grep", Arguments: `{"pattern":"TODO"}`}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "a.go:1:TODO"},
		{Role: llm.RoleAssistant, Content: "one TODO in a.go"},
	}
	if err := s.Append(turn[:2]...); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(turn[2:]...); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(s.Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info.Mode(), err)
	}
	if dir, _ := os.Stat(st.Dir); dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", dir.Mode())
	}
	got, err := Load(s.Path())
	if err != nil || len(got) != 4 || got[1].ToolCalls[0].Arguments != `{"pattern":"TODO"}` || got[2].ToolCallID != "c1" {
		t.Fatalf("loaded %+v, %v", got, err)
	}
}

func TestLoadSkipsDamagedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(`{"role":"user","content":"hi"}`+"\n"+`{"role":"assist`), 0o600)
	got, err := Load(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestListNewestFirst(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	old := st.New()
	old.Append(llm.Message{Role: llm.RoleUser, Content: "first idea"})
	newer := st.New()
	newer.Append(
		llm.Message{Role: llm.RoleUser, Content: "second idea"},
		llm.Message{Role: llm.RoleAssistant, Content: "ok"},
	)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old.Path(), past, past)

	infos, err := st.List()
	if err != nil || len(infos) != 2 {
		t.Fatalf("infos = %+v, %v", infos, err)
	}
	if infos[0].FirstPrompt != "second idea" || infos[0].Messages != 2 || infos[1].FirstPrompt != "first idea" {
		t.Fatalf("order/summary wrong: %+v", infos)
	}
}

func TestListMissingDir(t *testing.T) {
	infos, err := (&Store{Dir: filepath.Join(t.TempDir(), "none")}).List()
	if err != nil || infos != nil {
		t.Fatalf("got %v, %v", infos, err)
	}
}

func TestProjectKey(t *testing.T) {
	a, b := ProjectKey("/Volumes/notr/apps/også"), ProjectKey("/other/apps/også")
	if a == b || !strings.HasPrefix(a, "og") || a != ProjectKey("/Volumes/notr/apps/også") {
		t.Fatalf("keys: %q %q", a, b)
	}
	if strings.ContainsAny(a, "/ å") {
		t.Fatalf("unsafe characters in %q", a)
	}
}
