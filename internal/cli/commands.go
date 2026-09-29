package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/aldoportillo/ogsa/internal/config"
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
		p, err := r.pickProvider()
		if err != nil {
			return false, err
		}
		model, err := r.pickModel(p)
		if err != nil {
			return false, err
		}
		r.Config.Provider = p.Name
		r.Config.Model = model
		r.checkTools(false)
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

func (r *REPL) pickProvider() (config.Provider, error) {
	fmt.Fprintln(r.Out, "\nProviders:")
	for i, p := range r.Config.Providers {
		mark := " "
		if p.Name == r.Config.Provider {
			mark = "*"
		}
		fmt.Fprintf(r.Out, " %s %d) %-10s %s\n", mark, i+1, p.Name, p.BaseURL)
	}
	fmt.Fprintln(r.Out, "   a) add a provider")

	choice, err := r.prompt("provider [enter = current]: ")
	if err != nil {
		return config.Provider{}, err
	}
	switch {
	case choice == "":
		p, ok := r.Config.Active()
		if !ok {
			return config.Provider{}, errors.New("no active provider; choose one")
		}
		return p, nil
	case choice == "a":
		return r.addProvider()
	}
	i, err := strconv.Atoi(choice)
	if err != nil || i < 1 || i > len(r.Config.Providers) {
		return config.Provider{}, fmt.Errorf("invalid choice %q", choice)
	}
	return r.Config.Providers[i-1], nil
}

func (r *REPL) addProvider() (config.Provider, error) {
	var p config.Provider
	var err error
	if p.Name, err = r.prompt("name: "); err != nil {
		return p, err
	}
	if p.BaseURL, err = r.prompt("base URL (e.g. https://llm.example.com/v1): "); err != nil {
		return p, err
	}
	if p.APIKeyEnv, err = r.prompt("API key env var [enter = none]: "); err != nil {
		return p, err
	}
	if p.Name == "" || p.BaseURL == "" {
		return p, errors.New("name and base URL are required")
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	r.Config.Upsert(p)
	return p, nil
}

func (r *REPL) pickModel(p config.Provider) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := r.NewProvider(p).ListModels(ctx)
	if err != nil {
		fmt.Fprintf(r.Out, "could not list models from %s: %v\n", p.Name, err)
		return r.requireInput("model name: ")
	}
	if len(models) == 0 {
		fmt.Fprintf(r.Out, "%s reports no models\n", p.Name)
		return r.requireInput("model name: ")
	}

	fmt.Fprintf(r.Out, "\nModels on %s:\n", p.Name)
	for i, m := range models {
		fmt.Fprintf(r.Out, "  %d) %s\n", i+1, m)
	}
	choice, err := r.requireInput("model (number or name): ")
	if err != nil {
		return "", err
	}
	if i, err := strconv.Atoi(choice); err == nil {
		if i < 1 || i > len(models) {
			return "", fmt.Errorf("invalid choice %d", i)
		}
		return models[i-1], nil
	}
	return choice, nil
}

func (r *REPL) requireInput(label string) (string, error) {
	s, err := r.prompt(label)
	if err == nil && s == "" {
		err = errors.New("no model chosen")
	}
	return s, err
}
