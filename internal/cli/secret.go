package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"
)

func TerminalSecretReader(f *os.File, out io.Writer) func(string) (string, error) {
	return func(label string) (string, error) {
		fd := int(f.Fd())
		state, err := term.GetState(fd)
		if err != nil {
			return "", err
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		done := make(chan struct{})
		defer func() {
			signal.Stop(sig)
			close(done)
		}()
		go func() {
			select {
			case <-sig:
				term.Restore(fd, state)
				fmt.Fprintln(out)
				os.Exit(130)
			case <-done:
			}
		}()

		fmt.Fprint(out, label)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(out) // the user's Enter wasn't echoed
		return strings.TrimSpace(string(b)), err
	}
}
