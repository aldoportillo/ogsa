package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aldoportillo/ogsa/internal/agent"
)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "__pycache__": true, ".idea": true,
}

type Root struct {
	Dir  string
	root *os.Root
}

func OpenRoot(dir string) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	return &Root{Dir: abs, root: r}, nil
}

func (r *Root) Close() error { return r.root.Close() }

func (r *Root) ReadOnly() []agent.Tool {
	return []agent.Tool{globTool{r}, grepTool{r}, readTool{r}}
}

func (r *Root) Mutating() []agent.Tool {
	return []agent.Tool{editTool{r}, writeTool{r}}
}

func (r *Root) rel(path string) (string, error) {
	if path == "" {
		return ".", nil
	}
	if filepath.IsAbs(path) {
		p, err := filepath.Rel(r.Dir, path)
		if err != nil {
			return "", err
		}
		path = p
	}
	p := filepath.ToSlash(filepath.Clean(path))
	if !fs.ValidPath(p) {
		return "", fmt.Errorf("%s is outside the project directory", path)
	}
	return p, nil
}

func (r *Root) fs() fs.FS { return r.root.FS() }

func (r *Root) walk(start string, fn func(path string, d fs.DirEntry) error) error {
	return fs.WalkDir(r.fs(), start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == start {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if path != start && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(path, d)
	})
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

func decodeArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func limited(lines []string, total, max int, noun string) string {
	var b strings.Builder
	b.WriteString(strings.Join(lines, "\n"))
	if total > max {
		fmt.Fprintf(&b, "\n(%d more %s not shown; narrow the search)", total-max, noun)
	}
	return b.String()
}
