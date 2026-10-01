package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

var errCancel = errors.New("cancelled")

func (r *REPL) providerMenu() (p config.Provider, ok bool, err error) {
	for {
		r.printProviders()
		choice, err := r.prompt("choice: ")
		if err != nil {
			return p, false, err
		}
		choice = strings.ToLower(choice)
		switch {
		case choice == "q":
			return p, false, nil
		case choice == "":
			if p, ok := r.Config.Active(); ok {
				return p, true, nil
			}
			fmt.Fprintln(r.Out, "  there's no current provider yet; pick a number or press a to add one")
		case choice == "a":
			p, ok, err := r.addProvider()
			if err != nil || ok {
				return p, ok, err
			}
		case choice[0] == 'e' || choice[0] == 'r':
			i, err := r.providerIndex(strings.TrimSpace(choice[1:]))
			if err != nil {
				fmt.Fprintln(r.Out, "  ✗", err)
				continue
			}
			if choice[0] == 'e' {
				err = r.editProvider(i)
			} else {
				err = r.removeProvider(i)
			}
			if err != nil {
				return p, false, err
			}
		default:
			i, err := r.providerIndex(choice)
			if err != nil {
				fmt.Fprintln(r.Out, "  ✗", err)
				continue
			}
			return r.Config.Providers[i], true, nil
		}
	}
}

func (r *REPL) providerIndex(s string) (int, error) {
	n := len(r.Config.Providers)
	i, err := strconv.Atoi(s)
	switch {
	case n == 0:
		return 0, errors.New("there are no providers yet; press a to add one")
	case err != nil || i < 1 || i > n:
		return 0, fmt.Errorf("enter a provider number from 1 to %d", n)
	}
	return i - 1, nil
}

func (r *REPL) printProviders() {
	fmt.Fprintln(r.Out, "\nProviders")
	if len(r.Config.Providers) == 0 {
		fmt.Fprintln(r.Out, "  (none yet)")
	}
	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	for i, p := range r.Config.Providers {
		mark := " "
		if p.Name == r.Config.Provider {
			mark = "*"
		}
		fmt.Fprintf(tw, "  %s %d\t%s\t%s\t%s\n", mark, i+1, p.Name, p.BaseURL, keyStatus(p))
	}
	tw.Flush()

	current := "use current"
	if p, ok := r.Config.Active(); ok {
		current += " (" + p.Name + ")"
	}
	fmt.Fprintln(r.Out)
	tw = tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  <n>\tuse provider n\ta\tadd a provider")
	fmt.Fprintln(tw, "  e <n>\tedit provider n\tr <n>\tremove provider n")
	fmt.Fprintf(tw, "  enter\t%s\tq\tcancel\n", current)
	tw.Flush()
}

func keyStatus(p config.Provider) string {
	switch {
	case p.Key != "":
		return "key saved"
	case p.APIKeyEnv == "":
		return "no key"
	case !validKeyVar(p.APIKeyEnv):
		return "⚠ invalid key setting (edit to fix)"
	case os.Getenv(p.APIKeyEnv) == "":
		return "⚠ key $" + p.APIKeyEnv + " not set"
	default:
		return "key from $" + p.APIKeyEnv
	}
}

func validKeyVar(s string) bool { return config.ValidEnvName(s) && !looksLikeSecret(s) }

func looksLikeSecret(s string) bool {
	var lower, digit bool
	for _, c := range s {
		lower = lower || unicode.IsLower(c)
		digit = digit || unicode.IsDigit(c)
	}
	return len(s) >= 20 && digit && (lower || strings.Contains(s, "-"))
}

func (r *REPL) ask(label string, validate func(string) (string, error)) (string, error) {
	for {
		s, err := r.prompt(label)
		if err != nil {
			return "", err
		}
		if s == "q" {
			return "", errCancel
		}
		v, err := validate(s)
		if err == nil {
			return v, nil
		}
		fmt.Fprintln(r.Out, "  ✗", err)
	}
}

func (r *REPL) addProvider() (config.Provider, bool, error) {
	fmt.Fprintln(r.Out, "\nAdd a provider: a model on this machine (Ollama, LM Studio), in the cloud")
	fmt.Fprintln(r.Out, "(OpenAI, Anthropic, Azure, OpenRouter, …) or on your own server. Type q at any step to cancel.")
	p, err := r.providerForm(config.Provider{}, false)
	if errors.Is(err, errCancel) {
		fmt.Fprintln(r.Out, "  cancelled; nothing was saved")
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	if !r.confirmConnection(&p) {
		fmt.Fprintln(r.Out, "  not saved")
		return p, false, nil
	}
	r.Config.Upsert(p)
	if err := r.Config.Save(); err != nil {
		return p, false, fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(r.Out, "  ✓ added %s\n", p.Name)
	return p, true, nil
}

func (r *REPL) editProvider(i int) error {
	old := r.Config.Providers[i]
	fmt.Fprintf(r.Out, "\nEditing %s. Press enter to keep a value, q to cancel.\n", old.Name)
	p, err := r.providerForm(old, true)
	if errors.Is(err, errCancel) {
		fmt.Fprintln(r.Out, "  cancelled; nothing was changed")
		return nil
	}
	if err != nil {
		return err
	}
	if p == old {
		fmt.Fprintln(r.Out, "  no changes")
		return nil
	}
	p.API = llm.APIDetect
	if !r.confirmConnection(&p) {
		fmt.Fprintln(r.Out, "  not saved")
		return nil
	}
	r.Config.Replace(old.Name, p)
	if err := r.Config.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(r.Out, "  ✓ saved %s\n", p.Name)
	return nil
}

func (r *REPL) removeProvider(i int) error {
	p := r.Config.Providers[i]
	answer, err := r.prompt(fmt.Sprintf("remove %s (%s)? [y/N] ", p.Name, p.BaseURL))
	if err != nil {
		return err
	}
	if !isYes(answer) {
		fmt.Fprintln(r.Out, "  kept", p.Name)
		return nil
	}
	wasActive := p.Name == r.Config.Provider
	r.Config.Remove(p.Name)
	if err := r.Config.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintln(r.Out, "  ✓ removed", p.Name)
	if wasActive {
		fmt.Fprintln(r.Out, "  that was the current provider; pick another one to keep chatting")
	}
	return nil
}

func (r *REPL) providerForm(cur config.Provider, editing bool) (config.Provider, error) {
	var p config.Provider
	var err error

	fmt.Fprintln(r.Out, "\nName: a short label for this server, e.g. work-gpu")
	label := "name: "
	if editing {
		label = fmt.Sprintf("name [enter = keep %s]: ", cur.Name)
	}
	p.Name, err = r.ask(label, func(s string) (string, error) {
		switch {
		case s == "" && editing:
			return cur.Name, nil
		case s == "":
			return "", errors.New("a name is required")
		case strings.Contains(s, "/"):
			return "", errors.New("names can't contain /")
		case s != cur.Name:
			if _, exists := r.Config.Find(s); exists {
				return "", fmt.Errorf("a provider named %s already exists; use e <n> to edit it", s)
			}
		}
		return s, nil
	})
	if err != nil {
		return p, err
	}

	fmt.Fprintln(r.Out, "\nURL: where the model server is. ogsa works out the rest. Examples:")
	fmt.Fprintln(r.Out, "  Ollama on this machine  http://localhost:11434")
	fmt.Fprintln(r.Out, "  OpenAI                  https://api.openai.com/v1")
	fmt.Fprintln(r.Out, "  Anthropic               https://api.anthropic.com")
	fmt.Fprintln(r.Out, "  Azure AI Foundry        https://<resource>.services.ai.azure.com/anthropic")
	label = "base URL: "
	if editing {
		label = fmt.Sprintf("base URL [enter = keep %s]: ", cur.BaseURL)
	}
	var notes []string
	p.BaseURL, err = r.ask(label, func(s string) (string, error) {
		if s == "" && editing {
			s = cur.BaseURL
		}
		if s == "" {
			return "", errors.New("a base URL is required")
		}
		u, n, verr := normalizeBaseURL(s)
		notes = n
		return u, verr
	})
	if err != nil {
		return p, err
	}
	for _, n := range notes {
		fmt.Fprintln(r.Out, " ", n)
	}

	fmt.Fprintln(r.Out, "\nAPI key: paste it below; it won't show as you type. Leave it empty for servers")
	fmt.Fprintln(r.Out, "that don't need one, like Ollama on this machine.")
	hasKey := editing && (cur.Key != "" || validKeyVar(cur.APIKeyEnv))
	label = "API key [enter = none]: "
	switch {
	case hasKey:
		label = "API key [enter = keep current, - = remove]: "
	case editing && cur.APIKeyEnv != "":
		fmt.Fprintln(r.Out, "  the current key setting is invalid and will be replaced")
	}
	raw, err := r.askSecret(label, validateKeyInput)
	if err != nil {
		return p, err
	}
	switch {
	case raw == "" && hasKey:
		p.Key, p.APIKeyEnv = cur.Key, cur.APIKeyEnv
	case raw == "" || raw == "-":
	case strings.HasPrefix(raw, "$"):
		p.APIKeyEnv = raw[1:]
		if os.Getenv(p.APIKeyEnv) == "" {
			fmt.Fprintf(r.Out, "  ⚠ $%s is not set in this shell\n", p.APIKeyEnv)
		}
	default:
		p.Key = raw
		fmt.Fprintf(r.Out, "  the key will be saved in %s, readable only by you\n", r.Config.File())
	}
	return p, nil
}

func normalizeBaseURL(raw string) (string, []string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", nil, errors.New("enter a full URL starting with http:// or https://")
	}
	var notes []string
	for _, suffix := range []string{"/chat/completions", "/completions", "/messages", "/models"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			notes = append(notes, "removed "+suffix+" from the end: ogsa adds endpoint paths itself")
			break
		}
	}
	return u.String(), notes, nil
}

func (r *REPL) listModels(p config.Provider) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.NewProvider(p).ListModels(ctx)
}

type detector interface {
	Detect(context.Context) (llm.Detection, error)
}

func (r *REPL) confirmConnection(p *config.Provider) bool {
	fmt.Fprint(r.Out, "\nconnecting… ")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := r.NewProvider(*p)
	var err error
	if d, ok := client.(detector); ok {
		var det llm.Detection
		det, err = d.Detect(ctx)
		if det.API != llm.APIDetect {
			p.API = det.API
			if det.BaseURL != p.BaseURL {
				fmt.Fprintf(r.Out, "(using %s) ", det.BaseURL)
				p.BaseURL = det.BaseURL
			}
		}
	}
	var models []string
	if err == nil {
		models, err = client.ListModels(ctx)
	}
	switch {
	case err == nil:
		fmt.Fprintf(r.Out, "✓ connected%s, %d model(s) available\n", apiSuffix(p.API), len(models))
		return true
	case errors.Is(err, llm.ErrNoModelList):
		fmt.Fprintf(r.Out, "✓ found%s. This server doesn't list its models, so you'll type the model name next\n", apiSuffix(p.API))
		return true
	}
	fmt.Fprintln(r.Out, "✗")
	fmt.Fprintln(r.Out, " ", explainErr(err, *p))
	answer, err := r.prompt("save it anyway? [y/N] ")
	return err == nil && isYes(answer)
}

func explainErr(err error, p config.Provider) string {
	host := p.BaseURL
	if u, perr := url.Parse(p.BaseURL); perr == nil {
		host = u.Host
	}
	var httpErr *llm.HTTPError
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403):
		msg := fmt.Sprintf("authentication failed (%s). ", httpErr.Status)
		switch {
		case p.Key != "":
			return msg + "The saved key was rejected: edit the provider and paste a new one."
		case p.APIKeyEnv == "":
			return msg + "This server needs an API key: edit the provider and paste one."
		case !validKeyVar(p.APIKeyEnv):
			return msg + "The provider's key setting is invalid: edit the provider and paste the key."
		case os.Getenv(p.APIKeyEnv) == "":
			return msg + fmt.Sprintf("$%s is not set in this shell.", p.APIKeyEnv)
		default:
			return msg + fmt.Sprintf("Check that $%s holds a valid key for this server.", p.APIKeyEnv)
		}
	case errors.As(err, &httpErr) && httpErr.StatusCode == 404:
		return fmt.Sprintf("endpoint not found (404 for %s). Check the base URL: it should be the API root, usually ending in /v1.", httpErr.URL)
	case errors.As(err, &httpErr):
		return fmt.Sprintf("the server returned %s: %s", httpErr.Status, truncate(httpErr.Body, 200))
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("nothing is listening at %s. Is the server running?", host)
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("can't find the host %s. Check the URL for typos, or your network/VPN.", host)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("no response from %s within 10 seconds. The server may be down or unreachable (VPN?).", host)
	default:
		return err.Error()
	}
}

// pickModel lists p's models to choose from. ok is false if the user went back.
func (r *REPL) pickModel(p config.Provider) (model string, ok bool, err error) {
	current := ""
	if p.Name == r.Config.Provider {
		current = r.Config.Model
	}
	models, lerr := r.listModels(p)
	if errors.Is(lerr, llm.ErrNoModelList) {
		fmt.Fprintf(r.Out, "\n%s doesn't list its models. On Azure, use the deployment name.\n", p.Name)
		model, err = r.prompt("model name (enter = back): ")
		return model, err == nil && model != "", err
	}
	if lerr != nil {
		fmt.Fprintf(r.Out, "\n✗ couldn't list models from %s: %s\n", p.Name, explainErr(lerr, p))
		model, err = r.prompt("type a model name to use anyway, or press enter to go back: ")
		return model, err == nil && model != "", err
	}
	if len(models) == 0 {
		fmt.Fprintf(r.Out, "\n%s reports no models.\n", p.Name)
		model, err = r.prompt("type a model name to use anyway, or press enter to go back: ")
		return model, err == nil && model != "", err
	}

	fmt.Fprintf(r.Out, "\nModels on %s\n", p.Name)
	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	for i, m := range models {
		mark := " "
		if m == current {
			mark = "*"
		}
		tools := ""
		if t := r.Config.ModelInfo(p.Name, m).Tools; t != llm.ToolsUnknown {
			tools = "tools: " + string(t)
		}
		fmt.Fprintf(tw, "  %s %d\t%s\t%s\n", mark, i+1, m, tools)
	}
	tw.Flush()

	label := "model (number or name, enter = back): "
	if current != "" {
		label = fmt.Sprintf("model (number or name, enter = keep %s, b = back): ", current)
	}
	for {
		choice, err := r.prompt(label)
		if err != nil {
			return "", false, err
		}
		switch {
		case choice == "" && current != "":
			return current, true, nil
		case choice == "" || choice == "b":
			return "", false, nil
		}
		i, cerr := strconv.Atoi(choice)
		if cerr != nil {
			return choice, true, nil // a model name not in the list
		}
		if i >= 1 && i <= len(models) {
			return models[i-1], true, nil
		}
		fmt.Fprintf(r.Out, "  ✗ enter a number from 1 to %d, or a model name\n", len(models))
	}
}

func isYes(s string) bool {
	s = strings.ToLower(s)
	return s == "y" || s == "yes"
}

func apiSuffix(a llm.API) string {
	if a == llm.APIDetect {
		return ""
	}
	return " (" + a.String() + ")"
}

func validateKeyInput(s string) (string, error) {
	switch {
	case s == "" || s == "-":
		return s, nil
	case strings.HasPrefix(s, "$"):
		if !config.ValidEnvName(s[1:]) {
			return "", errors.New("after $, enter an environment variable name (letters, digits and _)")
		}
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		return "", errors.New("that looks like a URL, not a key")
	case strings.ContainsAny(s, " \t"):
		return "", errors.New("keys don't contain spaces; paste just the key")
	}
	return s, nil
}

func (r *REPL) askSecret(label string, validate func(string) (string, error)) (string, error) {
	for {
		s, err := r.readSecret(label)
		if err != nil {
			return "", err
		}
		if s == "q" {
			return "", errCancel
		}
		v, err := validate(s)
		if err == nil {
			return v, nil
		}
		fmt.Fprintln(r.Out, "  ✗", err)
	}
}

func (r *REPL) readSecret(label string) (string, error) {
	if r.ReadSecret == nil {
		return r.prompt(label)
	}
	return r.ReadSecret(label)
}
