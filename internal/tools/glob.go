package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"strings"

	"github.com/aldoportillo/ogsa/internal/llm"
)

const maxGlobResults = 200

type globTool struct{ r *Root }

func (globTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "glob",
		Description: "Find files by name pattern. Supports *, ?, ** (any number of directories) and {a,b}. " +
			"A pattern without a slash matches file names at any depth, e.g. \"*.go\"; " +
			"use \"src/**/*.ts\" to match by path. Returns paths relative to the project root.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Glob pattern, e.g. \"*.go\" or \"internal/**/*_test.go\""},
				"path": {"type": "string", "description": "Directory to search in, relative to the project root. Defaults to the root."}
			},
			"required": ["pattern"]
		}`),
	}
}

func (t globTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct{ Pattern, Path string }
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	if args.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	start, err := t.r.rel(args.Path)
	if err != nil {
		return "", err
	}
	match, err := compileGlob(args.Pattern)
	if err != nil {
		return "", err
	}

	var found []string
	total := 0
	err = t.r.walk(start, func(path string, _ fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if match(path) {
			total++
			if len(found) < maxGlobResults {
				found = append(found, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "no files matched", nil
	}
	return limited(found, total, maxGlobResults, "files"), nil
}

func compileGlob(pattern string) (func(string) bool, error) {
	re, err := regexp.Compile("^" + globToRegexp(pattern) + "$")
	if err != nil {
		return nil, errors.New("invalid pattern: " + pattern)
	}
	if !strings.Contains(pattern, "/") {
		return func(p string) bool { return re.MatchString(p[strings.LastIndexByte(p, '/')+1:]) }, nil
	}
	return re.MatchString, nil
}

func globToRegexp(g string) string {
	var b strings.Builder
	depth := 0 // inside {a,b}
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if strings.HasPrefix(g[i:], "**/") {
				b.WriteString("(?:.*/)?")
				i += 2
			} else if strings.HasPrefix(g[i:], "**") {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth == 0 {
				b.WriteString(`\}`)
				continue
			}
			depth--
			b.WriteString(")")
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
