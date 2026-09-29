package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aldoportillo/ogsa/internal/llm"
)

type Tool interface {
	Spec() llm.ToolSpec
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

type Hooks struct {
	OnText       func(delta string)
	OnToolCall   func(call llm.ToolCall)
	OnToolResult func(call llm.ToolCall, result string, err error)
}

const MaxSteps = 25

func Run(ctx context.Context, p llm.Provider, model string, tools []Tool, history []llm.Message, h Hooks) ([]llm.Message, error) {
	byName := make(map[string]Tool, len(tools))
	specs := make([]llm.ToolSpec, len(tools))
	for i, t := range tools {
		specs[i] = t.Spec()
		byName[specs[i].Name] = t
	}

	msgs := slices.Clone(history)
	start := len(msgs)
	for range MaxSteps {
		reply, err := p.Chat(ctx, llm.Request{Model: model, Messages: msgs, Tools: specs}, h.OnText)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			return msgs[start:], nil
		}
		for _, call := range reply.ToolCalls {
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Content:    runTool(ctx, byName, call, h),
			})
		}
	}
	return nil, fmt.Errorf("stopped after %d model calls without a final answer", MaxSteps)
}

func runTool(ctx context.Context, byName map[string]Tool, call llm.ToolCall, h Hooks) string {
	if h.OnToolCall != nil {
		h.OnToolCall(call)
	}
	var out string
	var err error
	if t, ok := byName[call.Name]; !ok {
		err = fmt.Errorf("unknown tool %q", call.Name)
	} else {
		args := json.RawMessage(call.Arguments)
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out, err = t.Run(ctx, args)
	}
	if h.OnToolResult != nil {
		h.OnToolResult(call, out, err)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}
