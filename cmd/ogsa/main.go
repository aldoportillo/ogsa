package main

import (
	"fmt"
	"os"

	"github.com/aldoportillo/ogsa/internal/cli"
	"github.com/aldoportillo/ogsa/internal/config"
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
	return cli.New(cfg, os.Stdin, os.Stdout).Run()
}
