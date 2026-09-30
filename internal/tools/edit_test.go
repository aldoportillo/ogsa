package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, r *Root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEditPreviewThenRun(t *testing.T) {
	r := project(t)
	args := json.RawMessage(`{"path":"main.go","old_string":"\t// TODO: wire up\n","new_string":"\trun()\n"}`)
	tool := editTool{r}

	diff, err := tool.Preview(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	want := "--- a/main.go\n+++ b/main.go\n@@ -1,5 +1,5 @@\n package main\n \n func main() {\n-\t// TODO: wire up\n+\trun()\n }"
	if diff != want {
		t.Fatalf("diff =\n%s\nwant\n%s", diff, want)
	}
	if strings.Contains(readFile(t, r, "main.go"), "run()") {
		t.Fatal("Preview modified the file")
	}

	out, err := tool.Run(context.Background(), args)
	if err != nil || out != "edited main.go (1 replacement(s))" {
		t.Fatalf("run = %q, %v", out, err)
	}
	if got := readFile(t, r, "main.go"); got != "package main\n\nfunc main() {\n\trun()\n}\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditErrors(t *testing.T) {
	r := project(t)
	os.WriteFile(filepath.Join(r.Dir, "dup.txt"), []byte("x\nx\n"), 0o644)
	for args, want := range map[string]string{
		`{"path":"main.go","old_string":"nope","new_string":"y"}`:     "not found",
		`{"path":"dup.txt","old_string":"x","new_string":"y"}`:        "appears 2 times",
		`{"path":"main.go","old_string":"","new_string":"y"}`:         "old_string is required",
		`{"path":"main.go","old_string":"main","new_string":"main"}`:  "identical",
		`{"path":"missing.go","old_string":"a","new_string":"b"}`:     "no such file",
		`{"path":".git/config","old_string":"TODO","new_string":"x"}`: "inside .git",
		`{"path":"../x","old_string":"a","new_string":"b"}`:           "outside the project",
	} {
		if _, err := (editTool{r}).Preview(context.Background(), json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
}

func TestEditReplaceAllKeepsMode(t *testing.T) {
	r := project(t)
	p := filepath.Join(r.Dir, "run.sh")
	os.WriteFile(p, []byte("echo a\necho a\n"), 0o755)
	out, err := (editTool{r}).Run(context.Background(), json.RawMessage(`{"path":"run.sh","old_string":"a","new_string":"b","replace_all":true}`))
	if err != nil || out != "edited run.sh (2 replacement(s))" {
		t.Fatalf("run = %q, %v", out, err)
	}
	info, _ := os.Stat(p)
	if readFile(t, r, "run.sh") != "echo b\necho b\n" || info.Mode().Perm() != 0o755 {
		t.Fatalf("file = %q mode = %v", readFile(t, r, "run.sh"), info.Mode())
	}
}

func TestWriteNewFile(t *testing.T) {
	r := project(t)
	args := json.RawMessage(`{"path":"docs/new/notes.md","content":"one\ntwo\n"}`)
	diff, err := (writeTool{r}).Preview(context.Background(), args)
	if err != nil || diff != "--- /dev/null\n+++ b/docs/new/notes.md\n@@ -0,0 +1,2 @@\n+one\n+two" {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "docs")); err == nil {
		t.Fatal("Preview created directories")
	}
	out, err := (writeTool{r}).Run(context.Background(), args)
	if err != nil || out != "created docs/new/notes.md (8 bytes)" || readFile(t, r, "docs/new/notes.md") != "one\ntwo\n" {
		t.Fatalf("run = %q, %v", out, err)
	}
}

func TestWriteOverwrite(t *testing.T) {
	r := project(t)
	args := json.RawMessage(`{"path":"README.md","content":"# demo\n\nMore.\n"}`)
	diff, err := (writeTool{r}).Preview(context.Background(), args)
	if err != nil || diff != "--- a/README.md\n+++ b/README.md\n@@ -1,1 +1,3 @@\n # demo\n+\n+More." {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	if out, err := (writeTool{r}).Run(context.Background(), args); err != nil || !strings.HasPrefix(out, "overwrote README.md") {
		t.Fatalf("run = %q, %v", out, err)
	}
}

func TestWriteRefusals(t *testing.T) {
	r := project(t)
	os.Symlink(filepath.Dir(r.Dir), filepath.Join(r.Dir, "up"))
	for args, want := range map[string]string{
		`{"path":".git/hooks/pre-commit","content":"x"}`: "inside .git",
		`{"path":"internal","content":"x"}`:              "is a directory",
		`{"path":"../escape.txt","content":"x"}`:         "outside the project",
		`{"path":"","content":"x"}`:                      "must be a file",
	} {
		if _, err := (writeTool{r}).Preview(context.Background(), json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
	// A symlink pointing outside passes the path check but os.Root blocks the write.
	if _, err := (writeTool{r}).Run(context.Background(), json.RawMessage(`{"path":"up/escape.txt","content":"x"}`)); err == nil {
		t.Error("write escaped through a symlink")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.Dir), "escape.txt")); err == nil {
		t.Error("file was created outside the project")
	}
}

func TestUnifiedDiff(t *testing.T) {
	tests := []struct{ before, after, want string }{
		{"a\nb\nc\n", "a\nb\nc\n", "(no changes)"},
		{"a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b"},
		// Only the middle is compared; changes interleave correctly.
		{"1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n", "@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8"},
		{"a\nb\nc\n", "a\nc\nd\n", "@@ -1,3 +1,3 @@\n a\n-b\n c\n+d"},
	}
	for _, tt := range tests {
		got := unifiedDiff("f", tt.before, tt.after, false)
		if _, body, ok := strings.Cut(got, "+++ b/f\n"); ok {
			got = body
		}
		if got != tt.want {
			t.Errorf("diff(%q, %q) =\n%s\nwant\n%s", tt.before, tt.after, got, tt.want)
		}
	}
}
