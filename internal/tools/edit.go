package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/aldoportillo/ogsa/internal/llm"
)

func (r *Root) writable(p string) (string, error) {
	rel, err := r.rel(p)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", errors.New("path must be a file")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" {
			return "", errors.New("refusing to modify files inside .git")
		}
	}
	return rel, nil
}

type editTool struct{ r *Root }

func (editTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "edit",
		Description: "Replace exact text in an existing file. old_string must match the file exactly, " +
			"including whitespace and indentation, and appear exactly once unless replace_all is true; " +
			"include enough surrounding lines to make it unique. Read the file first. " +
			"The user reviews a diff before the change is applied.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the project root"},
				"old_string": {"type": "string", "description": "Exact text to replace"},
				"new_string": {"type": "string", "description": "Replacement text"},
				"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring exactly one"}
			},
			"required": ["path", "old_string", "new_string"]
		}`),
	}
}

type editPlan struct {
	path, before, after string
	mode                fs.FileMode
	count               int
}

func (t editTool) plan(raw json.RawMessage) (editPlan, error) {
	var args struct {
		Path       string
		Old        string `json:"old_string"`
		New        string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return editPlan{}, err
	}
	if args.Old == "" {
		return editPlan{}, errors.New("old_string is required; use write to create a file")
	}
	if args.Old == args.New {
		return editPlan{}, errors.New("old_string and new_string are identical")
	}
	p, err := t.r.writable(args.Path)
	if err != nil {
		return editPlan{}, err
	}
	info, err := fs.Stat(t.r.fs(), p)
	if err != nil {
		return editPlan{}, err
	}
	data, err := fs.ReadFile(t.r.fs(), p)
	if err != nil {
		return editPlan{}, err
	}
	before := string(data)
	switch n := strings.Count(before, args.Old); {
	case n == 0:
		return editPlan{}, notFound(p, before, args.Old)
	case n > 1 && !args.ReplaceAll:
		return editPlan{}, fmt.Errorf("old_string appears %d times in %s; add surrounding lines to make it unique, or set replace_all", n, p)
	default:
		return editPlan{
			path:   p,
			before: before,
			after:  strings.ReplaceAll(before, args.Old, args.New),
			mode:   info.Mode().Perm(),
			count:  n,
		}, nil
	}
}

func (t editTool) Preview(_ context.Context, raw json.RawMessage) (string, error) {
	pl, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	return unifiedDiff(pl.path, pl.before, pl.after, false), nil
}

func (t editTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	pl, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	if err := t.r.root.WriteFile(pl.path, []byte(pl.after), pl.mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", pl.path, pl.count), nil
}

type writeTool struct{ r *Root }

func (writeTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "write",
		Description: "Create a file, or replace an existing file's entire contents. " +
			"Parent directories are created as needed. Prefer edit for changing part of an existing file. " +
			"The user reviews a diff before the change is applied.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the project root"},
				"content": {"type": "string", "description": "The complete file contents"}
			},
			"required": ["path", "content"]
		}`),
	}
}

type writePlan struct {
	path, before, content string
	mode                  fs.FileMode
	exists                bool
}

func (t writeTool) plan(raw json.RawMessage) (writePlan, error) {
	var args struct{ Path, Content string }
	if err := decodeArgs(raw, &args); err != nil {
		return writePlan{}, err
	}
	p, err := t.r.writable(args.Path)
	if err != nil {
		return writePlan{}, err
	}
	pl := writePlan{path: p, content: args.Content, mode: 0o644}
	info, err := fs.Stat(t.r.fs(), p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pl, nil
	case err != nil:
		return writePlan{}, err
	case info.IsDir():
		return writePlan{}, fmt.Errorf("%s is a directory", p)
	}
	data, err := fs.ReadFile(t.r.fs(), p)
	if err != nil {
		return writePlan{}, err
	}
	pl.before, pl.mode, pl.exists = string(data), info.Mode().Perm(), true
	return pl, nil
}

func (t writeTool) Preview(_ context.Context, raw json.RawMessage) (string, error) {
	pl, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	return unifiedDiff(pl.path, pl.before, pl.content, !pl.exists), nil
}

func (t writeTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	pl, err := t.plan(raw)
	if err != nil {
		return "", err
	}
	if dir := path.Dir(pl.path); dir != "." {
		if err := t.r.root.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := t.r.root.WriteFile(pl.path, []byte(pl.content), pl.mode); err != nil {
		return "", err
	}
	verb := "created"
	if pl.exists {
		verb = "overwrote"
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, pl.path, len(pl.content)), nil
}

var readPrefix = regexp.MustCompile(`(?m)^\s*\d+\|`)

func notFound(path, content, old string) error {
	if readPrefix.MatchString(old) && !readPrefix.MatchString(content) {
		return fmt.Errorf("old_string not found in %s: it contains line-number prefixes (like \"14|\") copied from read output; remove them", path)
	}
	fileLines, oldLines := splitLines(content), splitLines(old)
	if at, ok := matchIgnoringIndent(fileLines, oldLines); ok {
		for j, o := range oldLines {
			if j == 0 && leadingSpace(o) == "" {
				continue // old_string may start mid-line
			}
			if f := fileLines[at+j]; leadingSpace(f) != leadingSpace(o) {
				return fmt.Errorf("old_string not found in %s, but it matches line %d if whitespace is ignored. "+
					"Copy the indentation exactly: line %d starts with %s, your old_string has %s",
					path, at+1, at+j+1, describeIndent(f), describeIndent(o))
			}
		}
		return fmt.Errorf("old_string not found in %s, but it matches line %d if whitespace is ignored; check spaces and trailing whitespace", path, at+1)
	}
	return fmt.Errorf("old_string not found in %s; read the file and copy the text exactly", path)
}

func matchIgnoringIndent(file, old []string) (int, bool) {
	if strings.TrimSpace(strings.Join(old, "")) == "" {
		return 0, false
	}
	for i := 0; i+len(old) <= len(file); i++ {
		if linesMatch(file[i:i+len(old)], old) {
			return i, true
		}
	}
	return 0, false
}

func linesMatch(file, old []string) bool {
	last := len(old) - 1
	for j := range old {
		f, o := squash(file[j]), squash(old[j])
		var ok bool
		switch {
		case last == 0:
			ok = strings.Contains(f, o)
		case j == 0:
			ok = strings.HasSuffix(f, o)
		case j == last:
			ok = strings.HasPrefix(f, o)
		default:
			ok = f == o
		}
		if !ok {
			return false
		}
	}
	return true
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func describeIndent(s string) string {
	ws := leadingSpace(s)
	if ws == "" {
		return "no indentation"
	}
	count := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	tabs := strings.Count(ws, "\t")
	switch spaces := len(ws) - tabs; {
	case spaces == 0:
		return count(tabs, "tab")
	case tabs == 0:
		return count(spaces, "space")
	default:
		return count(tabs, "tab") + " and " + count(spaces, "space")
	}
}
