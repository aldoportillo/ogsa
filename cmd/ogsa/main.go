package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aldoportillo/ogsa/internal/cli"
	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/session"
	"github.com/aldoportillo/ogsa/internal/tools"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ogsa:", err)
		os.Exit(1)
	}
}

func run() error {
	cont := flag.Bool("c", false, "continue this project's most recent conversation")
	flag.BoolVar(cont, "continue", false, "same as -c")
	flag.Parse()

	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("loading %s: %w", path, err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := tools.OpenRoot(wd)
	if err != nil {
		return err
	}
	defer root.Close()

	r := cli.New(cfg, os.Stdin, os.Stdout)
	r.WorkDir = root.Dir
	r.Sessions = session.ForProject(filepath.Dir(cfg.File()), root.Dir)
	if *cont {
		if err := r.ContinueLast(); err != nil {
			return err
		}
	}
	r.Tools = append(root.ReadOnly(), root.Mutating()...)
	r.Color = isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""
	if isTerminal(os.Stdout) {
		r.Width = func() int {
			w, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				return 80
			}
			return w
		}
	}
	return r.Run()
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
