package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/aldoportillo/ogsa/internal/agent"
	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

type REPL struct {
	Config      *config.Config
	In          *bufio.Reader
	Out         io.Writer
	NewProvider func(config.Provider) llm.Provider
	Tools       []agent.Tool

	history []llm.Message
}

func New(cfg *config.Config, in io.Reader, out io.Writer) *REPL {
	return &REPL{
		Config: cfg,
		In:     bufio.NewReader(in),
		Out:    out,
		NewProvider: func(p config.Provider) llm.Provider {
			return llm.NewCompatClient(p.BaseURL, p.APIKey())
		},
	}
}

func (r *REPL) Run() error {
	fmt.Fprintln(r.Out, "ogsa — type /help for commands, /exit to quit")
	r.printCurrent()
	for {
		line, err := r.prompt("\n> ")
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(r.Out)
			return nil
		}
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			quit, err := r.command(line)
			if err != nil {
				fmt.Fprintln(r.Out, "error:", err)
			}
			if quit {
				return nil
			}
			continue
		}
		if err := r.chat(line); err != nil {
			fmt.Fprintln(r.Out, "\nerror:", err)
		}
	}
}

func (r *REPL) prompt(label string) (string, error) {
	fmt.Fprint(r.Out, label)
	line, err := r.In.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (r *REPL) printCurrent() {
	model := r.Config.Model
	if model == "" {
		model = "(none — run /model)"
	}
	fmt.Fprintf(r.Out, "provider: %s  model: %s", r.Config.Provider, model)
	if t := r.Config.ModelInfo(r.Config.Provider, r.Config.Model).Tools; t != llm.ToolsUnknown {
		fmt.Fprintf(r.Out, "  tools: %s", t)
	}
	fmt.Fprintln(r.Out)
}

func (r *REPL) activeProvider() (llm.Provider, error) {
	p, ok := r.Config.Active()
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured; run /model", r.Config.Provider)
	}
	return r.NewProvider(p), nil
}

func (r *REPL) chat(text string) error {
	if r.Config.Model == "" {
		return errors.New("no model selected; run /model")
	}
	provider, err := r.activeProvider()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tools := r.Tools
	if r.Config.ModelInfo(r.Config.Provider, r.Config.Model).Tools == llm.ToolsUnsupported {
		tools = nil
	}

	msgs := append(r.history, llm.Message{Role: llm.RoleUser, Content: text})
	turn, err := agent.Run(ctx, provider, r.Config.Model, tools, msgs, agent.Hooks{
		OnText: func(d string) { fmt.Fprint(r.Out, d) },
		OnToolCall: func(c llm.ToolCall) {
			fmt.Fprintf(r.Out, "\n→ %s %s\n", c.Name, c.Arguments)
		},
		OnToolResult: func(c llm.ToolCall, _ string, err error) {
			if err != nil {
				fmt.Fprintf(r.Out, "✗ %s: %v\n", c.Name, err)
			}
		},
	})
	fmt.Fprintln(r.Out)
	if ctx.Err() != nil {
		fmt.Fprintln(r.Out, "(cancelled)")
		return nil
	}
	if err != nil {
		return err
	}
	r.history = append(msgs, turn...)
	return nil
}
