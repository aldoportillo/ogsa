package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/aldoportillo/ogsa/internal/llm"
)

const (
	maxGrepMatches = 100
	maxGrepFile    = 2 << 20
	maxLineLen     = 300
)

type grepTool struct{ r *Root }

func (grepTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "grep",
		Description: "Search file contents with a regular expression (Go RE2 syntax). " +
			"Returns matching lines as path:line:content, where content starts right after the second colon. " +
			"Skips binary files and .git/node_modules.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Regular expression, e.g. \"func \\\\w+Handler\" or \"TODO\""},
				"path": {"type": "string", "description": "File or directory to search, relative to the project root. Defaults to the root."},
				"glob": {"type": "string", "description": "Only search files matching this glob, e.g. \"*.go\""},
				"ignore_case": {"type": "boolean", "description": "Case-insensitive match"}
			},
			"required": ["pattern"]
		}`),
	}
}

func (t grepTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Pattern, Path, Glob string
		IgnoreCase          bool `json:"ignore_case"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	if args.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	expr := args.Pattern
	if args.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}
	fileOK := func(string) bool { return true }
	if args.Glob != "" {
		if fileOK, err = compileGlob(args.Glob); err != nil {
			return "", err
		}
	}
	start, err := t.r.rel(args.Path)
	if err != nil {
		return "", err
	}

	var found []string
	total := 0
	err = t.r.walk(start, func(path string, d fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !fileOK(path) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxGrepFile {
			return nil
		}
		data, err := fs.ReadFile(t.r.fs(), path)
		if err != nil || isBinary(data) {
			return nil
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), maxGrepFile)
		for n := 1; sc.Scan(); n++ {
			if re.Match(sc.Bytes()) {
				total++
				if len(found) < maxGrepMatches {
					found = append(found, fmt.Sprintf("%s:%d:%s", path, n, truncateLine(sc.Text(), maxLineLen)))
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "no matches", nil
	}
	return limited(found, total, maxGrepMatches, "matches"), nil
}
