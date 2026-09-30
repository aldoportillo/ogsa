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
	"time"

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
	WorkDir     string
	Color       bool

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

	system := llm.Message{Role: llm.RoleSystem, Content: agent.SystemPrompt(r.WorkDir, len(tools) > 0, time.Now())}
	msgs := append(r.history, llm.Message{Role: llm.RoleUser, Content: text})
	turn, err := agent.Run(ctx, provider, r.Config.Model, tools, append([]llm.Message{system}, msgs...), r.hooks(ctx))
	fmt.Fprintln(r.Out)
	if ctx.Err() != nil {
		fmt.Fprintln(r.Out, "(cancelled)")
		return nil
	}
	if err != nil && !errors.Is(err, agent.ErrRepeatedFailure) {
		return err
	}
	r.history = append(msgs, turn...)
	return err
}

func (r *REPL) hooks(ctx context.Context) agent.Hooks {
	return agent.Hooks{
		OnText: func(d string) { fmt.Fprint(r.Out, d) },
		OnToolCall: func(c llm.ToolCall) {
			fmt.Fprintf(r.Out, "\n→ %s %s\n", c.Name, truncate(c.Arguments, 120))
		},
		OnToolResult: func(c llm.ToolCall, out string, err error) {
			switch {
			case errors.Is(err, agent.ErrDeclined):
				fmt.Fprintln(r.Out, "✗ declined")
			case err != nil:
				fmt.Fprintf(r.Out, "✗ %s: %v\n", c.Name, err)
			case r.isMutating(c.Name):
				fmt.Fprintln(r.Out, "✓", out)
			}
		},
		Approve: func(c llm.ToolCall, preview string) bool {
			fmt.Fprintln(r.Out, r.colorDiff(preview))
			answer, err := r.prompt(fmt.Sprintf("apply %s? [y/N] ", c.Name))
			if err != nil || ctx.Err() != nil {
				return false
			}
			answer = strings.ToLower(answer)
			return answer == "y" || answer == "yes"
		},
	}
}

func (r *REPL) isMutating(name string) bool {
	for _, t := range r.Tools {
		if t.Spec().Name == name {
			_, ok := t.(agent.Previewer)
			return ok
		}
	}
	return false
}

func (r *REPL) colorDiff(diff string) string {
	if !r.Color {
		return diff
	}
	lines := strings.Split(diff, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			lines[i] = "\x1b[1m" + l + "\x1b[0m"
		case strings.HasPrefix(l, "@@"):
			lines[i] = "\x1b[36m" + l + "\x1b[0m"
		case strings.HasPrefix(l, "+"):
			lines[i] = "\x1b[32m" + l + "\x1b[0m"
		case strings.HasPrefix(l, "-"):
			lines[i] = "\x1b[31m" + l + "\x1b[0m"
		}
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
