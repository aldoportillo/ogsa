package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/aldoportillo/ogsa/internal/llm"
)

const (
	defaultReadLines = 400
	maxReadFile      = 10 << 20
	maxReadLineLen   = 2000
)

type readTool struct{ r *Root }

func (readTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "read",
		Description: "Read a text file. Each output line is \"<line number>|<content>\": the file's text starts " +
			"immediately after the |, including its indentation. Never copy the number or | into an edit. " +
			fmt.Sprintf("Returns at most %d lines per call; use offset to continue.", defaultReadLines),
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the project root"},
				"offset": {"type": "integer", "description": "Line number to start from (1-based). Defaults to 1."},
				"limit": {"type": "integer", "description": "Maximum number of lines to return"}
			},
			"required": ["path"]
		}`),
	}
}

func (t readTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path          string
		Offset, Limit int
	}
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	if args.Path == "" {
		return "", errors.New("path is required")
	}
	path, err := t.r.rel(args.Path)
	if err != nil {
		return "", err
	}
	info, err := fs.Stat(t.r.fs(), path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use glob to list files", args.Path)
	}
	if info.Size() > maxReadFile {
		return "", fmt.Errorf("%s is too large (%d bytes); use grep to find the relevant lines", args.Path, info.Size())
	}
	data, err := fs.ReadFile(t.r.fs(), path)
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s is a binary file", args.Path)
	}

	offset := max(args.Offset, 1)
	limit := args.Limit
	if limit <= 0 || limit > defaultReadLines {
		limit = defaultReadLines
	}

	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), maxReadFile)
	n, shown := 0, 0
	for sc.Scan() {
		n++
		if n < offset {
			continue
		}
		if shown == limit {
			continue
		}
		fmt.Fprintf(&b, "%6d|%s\n", n, truncateLine(sc.Text(), maxReadLineLen))
		shown++
	}
	switch {
	case n == 0:
		return "(empty file)", nil
	case offset > n:
		return "", fmt.Errorf("offset %d is past the end of the file (%d lines)", offset, n)
	case offset+shown-1 < n:
		fmt.Fprintf(&b, "(showing lines %d-%d of %d; use offset=%d to continue)", offset, offset+shown-1, n, offset+shown)
	}
	return b.String(), nil
}
