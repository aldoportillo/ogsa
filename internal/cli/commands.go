package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/aldoportillo/ogsa/internal/llm"
)

type command struct {
	name, usage, help string
	run               func(r *REPL, args string) (quit bool, err error)
}

var commands []command

func init() {
	commands = []command{
		{"/model", "/model [name|check]", "pick a provider and model, set one directly, or re-check tool support", (*REPL).cmdModel},
		{"/clear", "/clear", "clear the conversation history", (*REPL).cmdClear},
		{"/help", "/help", "show this help", (*REPL).cmdHelp},
		{"/exit", "/exit", "quit ogsa", func(*REPL, string) (bool, error) { return true, nil }},
	}
}

func (r *REPL) command(line string) (bool, error) {
	name, args, _ := strings.Cut(line, " ")
	if name == "/quit" {
		name = "/exit"
	}
	for _, c := range commands {
		if c.name == name {
			return c.run(r, strings.TrimSpace(args))
		}
	}
	return false, fmt.Errorf("unknown command %s (try /help)", name)
}

func (r *REPL) cmdHelp(string) (bool, error) {
	for _, c := range commands {
		fmt.Fprintf(r.Out, "  %-20s %s\n", c.usage, c.help)
	}
	return false, nil
}

func (r *REPL) cmdClear(string) (bool, error) {
	r.history = nil
	fmt.Fprintln(r.Out, "history cleared")
	return false, nil
}

func (r *REPL) cmdModel(args string) (bool, error) {
	switch args {
	case "":
		r.printCurrent()
		for {
			p, ok, err := r.providerMenu()
			if err != nil {
				return false, err
			}
			if !ok {
				fmt.Fprintln(r.Out, "model unchanged")
				return false, nil
			}
			model, ok, err := r.pickModel(p)
			if err != nil {
				return false, err
			}
			if ok {
				r.Config.Provider, r.Config.Model = p.Name, model
				r.checkTools(false)
				break
			}
		}
	case "check":
		if r.Config.Model == "" {
			return false, errors.New("no model selected")
		}
		r.checkTools(true)
	default:
		r.Config.Model = args
		r.checkTools(false)
	}
	return false, r.saveAndShow()
}

func (r *REPL) checkTools(force bool) {
	prov, model := r.Config.Provider, r.Config.Model
	info := r.Config.ModelInfo(prov, model)
	if !force && info.Tools != llm.ToolsUnknown {
		return
	}
	provider, err := r.activeProvider()
	if err != nil {
		fmt.Fprintln(r.Out, "error:", err)
		return
	}

	fmt.Fprintf(r.Out, "checking tool support for %s… (Ctrl-C to skip)\n", model)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute) // first run may load the model
	defer cancel()

	support, err := llm.CheckTools(ctx, provider, model)
	if errors.Is(ctx.Err(), context.Canceled) {
		fmt.Fprintln(r.Out, "skipped")
		return
	}
	if err != nil {
		fmt.Fprintln(r.Out, "could not check tool support:", err)
		return
	}
	info.Tools = support
	r.Config.SetModelInfo(prov, model, info)

	switch support {
	case llm.ToolsUnsupported:
		fmt.Fprintf(r.Out, "⚠ %s does not support tool calling; ogsa will use it for plain chat only\n", model)
	case llm.ToolsUnreliable:
		fmt.Fprintf(r.Out, "⚠ %s accepted tools but answered in text instead of calling one; tool use may not work\n", model)
	}
}

func (r *REPL) saveAndShow() error {
	if err := r.Config.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	r.printCurrent()
	return nil
}
