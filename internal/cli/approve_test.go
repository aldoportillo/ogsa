package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
	"github.com/aldoportillo/ogsa/internal/tools"
)

type editOnce struct{ result *string }

func (editOnce) ListModels(context.Context) ([]string, error) { return nil, nil }

func (editOnce) CheckTools(context.Context, string) (llm.ToolSupport, error) {
	return llm.ToolsSupported, nil
}

func (e editOnce) Chat(_ context.Context, req llm.Request, _ func(string)) (llm.Message, error) {
	last := req.Messages[len(req.Messages)-1]
	if last.Role == llm.RoleTool {
		*e.result = last.Content
		return llm.Message{Role: llm.RoleAssistant, Content: "done"}, nil
	}
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
		ID: "c1", Name: "edit", Arguments: `{"path":"a.txt","old_string":"hello","new_string":"bye"}`,
	}}}, nil
}

func TestEditApproval(t *testing.T) {
	for _, tt := range []struct {
		answer, wantFile, wantResult, wantOut string
	}{
		{"y", "bye\n", "edited a.txt (1 replacement(s))", "✓ edited a.txt"},
		{"n", "hello\n", "error: the user declined", "✗ declined"},
		{"", "hello\n", "error: the user declined", "✗ declined"}, // default is no
	} {
		t.Run("answer "+tt.answer, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
			root, err := tools.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.json"))
			var out strings.Builder
			var result string
			r := New(cfg, strings.NewReader("/model m\nchange it\n"+tt.answer+"\n"), &out)
			r.Tools = append(root.ReadOnly(), root.Mutating()...)
			r.NewProvider = func(config.Provider) llm.Provider { return editOnce{&result} }
			if err := r.Run(); err != nil {
				t.Fatal(err)
			}

			got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
			if string(got) != tt.wantFile || !strings.HasPrefix(result, tt.wantResult) {
				t.Fatalf("file = %q, result = %q", got, result)
			}
			o := out.String()
			if !strings.Contains(o, "-hello\n+bye") || !strings.Contains(o, "apply edit? [y/N]") || !strings.Contains(o, tt.wantOut) {
				t.Fatalf("output:\n%s", o)
			}
		})
	}
}
