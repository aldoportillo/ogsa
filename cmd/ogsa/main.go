package main

import (
	"fmt"
	"os"

	"github.com/aldoportillo/ogsa/internal/cli"
	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/tools"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ogsa:", err)
		os.Exit(1)
	}
}

func run() error {
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
	r.Tools = append(root.ReadOnly(), root.Mutating()...)
	r.Color = isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""
	if isTerminal(os.Stdin) {
		r.ReadSecret = cli.TerminalSecretReader(os.Stdin, os.Stdout)
	}
	return r.Run()
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
