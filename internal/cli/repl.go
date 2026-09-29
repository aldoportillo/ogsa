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

	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

type REPL struct {
	Config      *config.Config
	In          *bufio.Reader
	Out         io.Writer
	NewProvider func(config.Provider) llm.Provider

	history []llm.Message
}

func New(cfg *config.Config, in io.Reader, out io.Writer) *REPL {
	return &REPL{
		Config: cfg,
		In:     bufio.NewReader(in),
		Out:    out,
		NewProvider: func(p config.Provider) llm.Provider {
			return llm.NewOpenAICompat(p.BaseURL, p.APIKey())
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
	fmt.Fprintf(r.Out, "provider: %s  model: %s\n", r.Config.Provider, model)
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

	msgs := append(r.history, llm.Message{Role: llm.RoleUser, Content: text})
	reply, err := provider.Chat(ctx, llm.Request{Model: r.Config.Model, Messages: msgs}, func(d string) {
		fmt.Fprint(r.Out, d)
	})
	fmt.Fprintln(r.Out)
	if ctx.Err() != nil {
		fmt.Fprintln(r.Out, "(cancelled)")
		return nil
	}
	if err != nil {
		return err
	}
	r.history = append(msgs, reply)
	return nil
}
