package agent

import (
	"context"
	"encoding/json"
	"errors"
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
	Approve      func(call llm.ToolCall, preview string) bool
}

type Previewer interface {
	Preview(ctx context.Context, args json.RawMessage) (string, error)
}

var ErrDeclined = errors.New("the user declined this action; ask them what they would like instead")

const MaxSteps = 25

var ErrRepeatedFailure = errors.New("stopped: the model repeated the same failing tool call 3 times")

const repeatNote = "\n\nYou have made this exact call before and it failed the same way. Do not repeat it: " +
	"change the arguments based on the error, try a different approach, or explain the problem to the user."

func Run(ctx context.Context, p llm.Provider, model string, tools []Tool, history []llm.Message, h Hooks) ([]llm.Message, error) {
	byName := make(map[string]Tool, len(tools))
	specs := make([]llm.ToolSpec, len(tools))
	for i, t := range tools {
		specs[i] = t.Spec()
		byName[specs[i].Name] = t
	}

	msgs := slices.Clone(history)
	start := len(msgs)
	failures := map[string]int{}
	for range MaxSteps {
		reply, err := p.Chat(ctx, llm.Request{Model: model, Messages: msgs, Tools: specs}, h.OnText)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			return msgs[start:], nil
		}
		repeated := false
		for _, call := range reply.ToolCalls {
			content, failed := runTool(ctx, byName, call, h)
			if failed {
				key := call.Name + "\x00" + call.Arguments
				failures[key]++
				switch n := failures[key]; {
				case n >= 3:
					repeated = true
				case n == 2:
					content += repeatNote
				}
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: content})
		}
		if repeated {
			return msgs[start:], ErrRepeatedFailure
		}
	}
	return nil, fmt.Errorf("stopped after %d model calls without a final answer", MaxSteps)
}

func runTool(ctx context.Context, byName map[string]Tool, call llm.ToolCall, h Hooks) (string, bool) {
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
		out, err = runApproved(ctx, t, call, args, h)
	}
	if h.OnToolResult != nil {
		h.OnToolResult(call, out, err)
	}
	if err != nil {
		return "error: " + err.Error(), true
	}
	return out, false
}

func runApproved(ctx context.Context, t Tool, call llm.ToolCall, args json.RawMessage, h Hooks) (string, error) {
	if p, ok := t.(Previewer); ok {
		preview, err := p.Preview(ctx, args)
		if err != nil {
			return "", err
		}
		if h.Approve == nil || !h.Approve(call, preview) {
			return "", ErrDeclined
		}
	}
	return t.Run(ctx, args)
}
