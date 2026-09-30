package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func project(t *testing.T) *Root {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"main.go":                     "package main\n\nfunc main() {\n\t// TODO: wire up\n}\n",
		"README.md":                   "# demo\n",
		"internal/llm/compat.go":      "package llm\n\nfunc NewCompatClient() {}\n",
		"internal/llm/compat_test.go": "package llm\n",
		"web/app.ts":                  "// todo: styles\nexport {}\n",
		".git/config":                 "TODO in git\n",
		"node_modules/x/index.js":     "TODO in deps\n",
		"bin/blob":                    "TODO\x00binary",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func call(t *testing.T, tool interface {
	Run(context.Context, json.RawMessage) (string, error)
}, args string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(args))
}

func TestGlob(t *testing.T) {
	r := project(t)
	tests := []struct{ args, want string }{
		{`{"pattern":"*.go"}`, "internal/llm/compat.go\ninternal/llm/compat_test.go\nmain.go"},
		{`{"pattern":"internal/**/*_test.go"}`, "internal/llm/compat_test.go"},
		{`{"pattern":"**/*.{md,ts}"}`, "README.md\nweb/app.ts"},
		{`{"pattern":"*.go","path":"internal"}`, "internal/llm/compat.go\ninternal/llm/compat_test.go"},
		{`{"pattern":"*.js"}`, "no files matched"}, // node_modules is skipped
	}
	for _, tt := range tests {
		got, err := call(t, globTool{r}, tt.args)
		if err != nil || got != tt.want {
			t.Errorf("glob %s = %q, %v; want %q", tt.args, got, err, tt.want)
		}
	}
}

func TestGlobToRegexp(t *testing.T) {
	tests := []struct {
		glob, path string
		want       bool
	}{
		{"**/*.go", "a.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"src/*.go", "src/a/b.go", false},
		{"{a,b}.txt,c", "b.txt,c", true},
		{"a?c", "abc", true},
		{"a.c", "abc", false}, // dots are literal
	}
	for _, tt := range tests {
		match, err := compileGlob(tt.glob)
		if err != nil {
			t.Fatal(err)
		}
		if got := match(tt.path); got != tt.want {
			t.Errorf("%q vs %q = %v, want %v", tt.glob, tt.path, got, tt.want)
		}
	}
}

func TestGrep(t *testing.T) {
	r := project(t)
	tests := []struct{ args, want string }{
		{`{"pattern":"TODO"}`, "main.go:4:\t// TODO: wire up"},
		{`{"pattern":"todo","ignore_case":true,"glob":"*.ts"}`, "web/app.ts:1:// todo: styles"},
		{`{"pattern":"func \\w+","path":"internal"}`, "internal/llm/compat.go:3:func NewCompatClient() {}"},
		{`{"pattern":"nothing-here"}`, "no matches"},
	}
	for _, tt := range tests {
		got, err := call(t, grepTool{r}, tt.args)
		if err != nil || got != tt.want {
			t.Errorf("grep %s = %q, %v; want %q", tt.args, got, err, tt.want)
		}
	}
	if _, err := call(t, grepTool{r}, `{"pattern":"("}`); err == nil {
		t.Error("invalid regexp accepted")
	}
}

func TestGrepLimit(t *testing.T) {
	r := project(t)
	body := strings.Repeat("hit\n", maxGrepMatches+5)
	os.WriteFile(filepath.Join(r.Dir, "many.txt"), []byte(body), 0o644)
	got, err := call(t, grepTool{r}, `{"pattern":"hit"}`)
	if err != nil || !strings.HasSuffix(got, "(5 more matches not shown; narrow the search)") {
		t.Fatalf("got %q, %v", got[max(0, len(got)-80):], err)
	}
}

func TestRead(t *testing.T) {
	r := project(t)
	got, err := call(t, readTool{r}, `{"path":"main.go"}`)
	if err != nil || !strings.HasPrefix(got, "     1|package main\n") || !strings.Contains(got, "     4|\t// TODO") {
		t.Fatalf("read = %q, %v", got, err)
	}

	got, err = call(t, readTool{r}, `{"path":"main.go","offset":2,"limit":2}`)
	want := "     2|\n     3|func main() {\n(showing lines 2-3 of 5; use offset=4 to continue)"
	if err != nil || got != want {
		t.Fatalf("paged read = %q, %v", got, err)
	}

	abs := filepath.Join(r.Dir, "README.md")
	if got, err := call(t, readTool{r}, fmt.Sprintf(`{"path":%q}`, abs)); err != nil || got != "     1|# demo\n" {
		t.Fatalf("absolute path read = %q, %v", got, err)
	}
}

func TestReadErrors(t *testing.T) {
	r := project(t)
	for args, want := range map[string]string{
		`{"path":"internal"}`:            "is a directory",
		`{"path":"bin/blob"}`:            "binary file",
		`{"path":"missing.go"}`:          "no such file",
		`{"path":"main.go","offset":99}`: "past the end",
		`{}`:                             "path is required",
	} {
		if _, err := call(t, readTool{r}, args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("read %s: err = %v, want %q", args, err, want)
		}
	}
}

func TestCannotEscapeRoot(t *testing.T) {
	r := project(t)
	outside := filepath.Join(filepath.Dir(r.Dir), "secret.txt")
	os.WriteFile(outside, []byte("secret"), 0o644)
	t.Cleanup(func() { os.Remove(outside) })
	os.Symlink(outside, filepath.Join(r.Dir, "link.txt"))
	os.Symlink(filepath.Dir(r.Dir), filepath.Join(r.Dir, "linkdir"))

	for _, args := range []string{
		`{"path":"../secret.txt"}`,
		fmt.Sprintf(`{"path":%q}`, outside),
		`{"path":"link.txt"}`,
		`{"path":"linkdir/secret.txt"}`,
	} {
		if got, err := call(t, readTool{r}, args); err == nil {
			t.Errorf("read %s escaped the root: %q", args, got)
		}
	}
	if _, err := call(t, grepTool{r}, `{"pattern":"secret","path":".."}`); err == nil {
		t.Error("grep escaped the root")
	}
}
