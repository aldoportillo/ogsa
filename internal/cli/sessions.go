package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aldoportillo/ogsa/internal/session"
)

const resumeListSize = 10

func (r *REPL) ContinueLast() error {
	infos, err := r.listSessions()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintln(r.Out, "no previous session for this project; starting a new one")
		return nil
	}
	return r.resume(infos[0])
}

func (r *REPL) hintLastSession() {
	infos, err := r.listSessions()
	if err != nil || len(infos) == 0 {
		return
	}
	fmt.Fprintf(r.Out, "last session: %s, %q — /resume or ogsa -c to continue it\n",
		ago(infos[0].Modified), truncate(oneLine(infos[0].FirstPrompt), 50))
}

func (r *REPL) cmdResume(string) (bool, error) {
	infos, err := r.listSessions()
	if err != nil {
		return false, err
	}
	if len(infos) == 0 {
		fmt.Fprintln(r.Out, "no saved sessions for this project yet")
		return false, nil
	}
	infos = infos[:min(len(infos), resumeListSize)]
	fmt.Fprintln(r.Out, "\nRecent sessions")
	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	for i, in := range infos {
		mark := " "
		if r.session != nil && r.session.Path() == in.Path {
			mark = "*"
		}
		fmt.Fprintf(tw, "  %s %d\t%s\t%d messages\t%s\n", mark, i+1, ago(in.Modified), in.Messages, truncate(oneLine(in.FirstPrompt), 60))
	}
	tw.Flush()
	choice, err := r.prompt("session (enter = cancel): ")
	if err != nil || choice == "" {
		return false, err
	}
	i, err := strconv.Atoi(choice)
	if err != nil || i < 1 || i > len(infos) {
		return false, fmt.Errorf("enter a number from 1 to %d", len(infos))
	}
	return false, r.resume(infos[i-1])
}

func (r *REPL) resume(in session.Info) error {
	msgs, err := session.Load(in.Path)
	if err != nil {
		return err
	}
	r.history = msgs
	r.session = r.Sessions.Open(in.Path)
	fmt.Fprintf(r.Out, "resumed session from %s (%d messages): %q\n", ago(in.Modified), len(msgs), truncate(oneLine(in.FirstPrompt), 60))
	return nil
}

func (r *REPL) listSessions() ([]session.Info, error) {
	if r.Sessions == nil {
		return nil, nil
	}
	return r.Sessions.List()
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour && t.Day() == time.Now().Day():
		return "today " + t.Format("15:04")
	case d < 48*time.Hour:
		return "yesterday " + t.Format("15:04")
	default:
		return t.Format("Jan 2 15:04")
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
