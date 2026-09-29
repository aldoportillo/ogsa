package llm

import (
	"context"
	"encoding/json"
	"strings"
)

type ToolSupport string

const (
	ToolsUnknown     ToolSupport = ""
	ToolsSupported   ToolSupport = "supported"
	ToolsUnsupported ToolSupport = "unsupported"
	ToolsUnreliable  ToolSupport = "unreliable"
)

type ToolChecker interface {
	CheckTools(ctx context.Context, model string) (ToolSupport, error)
}

func CheckTools(ctx context.Context, p Provider, model string) (ToolSupport, error) {
	if tc, ok := p.(ToolChecker); ok {
		return tc.CheckTools(ctx, model)
	}
	return ProbeTools(ctx, p, model)
}

const probeTool = "get_current_time"

func ProbeTools(ctx context.Context, p Provider, model string) (ToolSupport, error) {
	reply, err := p.Chat(ctx, Request{
		Model: model,
		Messages: []Message{
			{Role: RoleUser, Content: "What time is it right now? You must call the " + probeTool + " tool to find out."},
		},
		Tools: []ToolSpec{{
			Name:        probeTool,
			Description: "Returns the current time.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"timezone":{"type":"string","description":"IANA timezone, e.g. UTC"}}}`),
		}},
	}, nil)
	if err != nil {
		if ctx.Err() == nil && strings.Contains(strings.ToLower(err.Error()), "tool") {
			return ToolsUnsupported, nil
		}
		return ToolsUnknown, err
	}
	for _, c := range reply.ToolCalls {
		if c.Name == probeTool {
			return ToolsSupported, nil
		}
	}
	return ToolsUnreliable, nil
}
