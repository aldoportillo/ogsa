package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldoportillo/ogsa/internal/config"
	"github.com/aldoportillo/ogsa/internal/llm"
)

const fakeSecret = "8f3kQ9x2Lm4Np7Rs1Tv6Wy0Za5Bc3De"

func runSession(t *testing.T, input string, setup func(*config.Config)) (string, *config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(cfg)
	}
	var out strings.Builder
	var seen []llm.Request
	var checks int
	r := New(cfg, strings.NewReader(input), &out)
	r.NewProvider = func(p config.Provider) llm.Provider {
		return fakeProvider{baseURL: p.BaseURL, models: []string{"small", "big"}, seen: &seen, support: llm.ToolsSupported, checks: &checks}
	}
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), saved
}

func TestAddStoresPastedKey(t *testing.T) {
	input := "/model\na\nwork\nhttps://llm.work/v1\n" + fakeSecret + "\n1\n/model\nq\n"
	out, cfg := runSession(t, input, nil)
	if strings.Contains(out, fakeSecret) {
		t.Fatal("the key was printed")
	}
	p, ok := cfg.Find("work")
	if !ok || p.Key != fakeSecret || p.APIKeyEnv != "" {
		t.Fatalf("key not stored (found=%v, stored=%v)", ok, p.Key == fakeSecret)
	}
	for _, want := range []string{"cleared from the screen once entered", "readable only by you", "key saved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}
func TestAddValidatesAndNormalizes(t *testing.T) {
	input := "/model\na\n\nollama\nwork\nlocalhost:8000\nhttp://gpu:8000/v1/chat/completions/\n$WORK_KEY\n2\n"
	out, cfg := runSession(t, input, nil)
	for _, want := range []string{
		"a name is required",
		"already exists",
		"starting with http:// or https://",
		"removed /chat/completions",
		"✓ connected, 2 model(s) available",
		"✓ added work",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	p, _ := cfg.Find("work")
	if p.BaseURL != "http://gpu:8000/v1" || p.APIKeyEnv != "WORK_KEY" {
		t.Fatalf("provider = %+v", p)
	}
	if cfg.Provider != "work" || cfg.Model != "big" {
		t.Fatalf("selection = %s/%s", cfg.Provider, cfg.Model)
	}
}

func TestAddCancel(t *testing.T) {
	out, cfg := runSession(t, "/model\na\nwork\nq\nq\n", nil)
	if _, ok := cfg.Find("work"); ok || !strings.Contains(out, "cancelled; nothing was saved") {
		t.Fatalf("cancel failed:\n%s", out)
	}
}

func TestRemoveProvider(t *testing.T) {
	setup := func(c *config.Config) {
		c.Upsert(config.Provider{Name: "cloud", BaseURL: "https://x/v1", Key: fakeSecret})
	}
	out, cfg := runSession(t, "/model\nr 2\ny\nr1\nn\nq\n", setup)
	if _, ok := cfg.Find("cloud"); ok {
		t.Fatal("cloud not removed")
	}
	raw, _ := os.ReadFile(cfg.File())
	if strings.Contains(string(raw), fakeSecret) {
		t.Fatal("the removed provider's key is still in the config file")
	}
	if _, ok := cfg.Find("ollama"); !ok || !strings.Contains(out, "kept ollama") {
		t.Fatalf("declined removal removed it anyway:\n%s", out)
	}
}
func TestRemoveActiveProviderClearsSelection(t *testing.T) {
	out, cfg := runSession(t, "/model\nr 1\ny\nq\n", func(c *config.Config) { c.Model = "small" })
	if cfg.Provider != "" || cfg.Model != "" || !strings.Contains(out, "that was the current provider") {
		t.Fatalf("selection = %q/%q\n%s", cfg.Provider, cfg.Model, out)
	}
}

func TestEditProvider(t *testing.T) {
	setup := func(c *config.Config) {
		c.Upsert(config.Provider{Name: "cloud", BaseURL: "https://x/v1", Key: fakeSecret})
	}
	tests := []struct {
		name, keyInput, wantKey, wantURL string
	}{
		{"enter keeps the key", "", fakeSecret, "https://y/v1"},
		{"dash removes it", "-", "", "https://y/v1"},
		{"a new key replaces it", "new-key-123", "new-key-123", "https://y/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, cfg := runSession(t, "/model\ne 2\n\nhttps://y/v1\n"+tt.keyInput+"\nq\n", setup)
			p, _ := cfg.Find("cloud")
			if p.Key != tt.wantKey || p.BaseURL != tt.wantURL || !strings.Contains(out, "✓ saved cloud") {
				t.Fatalf("url = %q, key matches = %v\n%s", p.BaseURL, p.Key == tt.wantKey, out)
			}
			if strings.Contains(out, fakeSecret) {
				t.Fatal("the key was printed")
			}
		})
	}
}
func TestInvalidKeySettingIsNeverShown(t *testing.T) {
	setup := func(c *config.Config) {
		c.Upsert(config.Provider{Name: "Azure", BaseURL: "https://x.example/v1", APIKeyEnv: fakeSecret})
		c.Provider, c.Model = "Azure", "m"
	}
	out, cfg := runSession(t, "/model\ne 2\n\n\nreal-key-456\nq\n", setup)
	if strings.Contains(out, fakeSecret) {
		t.Fatal("the stored key was printed")
	}
	for _, want := range []string{"invalid key setting", "run /model to fix it", "will be replaced"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if p, _ := cfg.Find("Azure"); p.Key != "real-key-456" || p.APIKeyEnv != "" {
		t.Fatal("key setting was not replaced")
	}
}
func TestFailedConnectionAsksBeforeSaving(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := config.Load(path)
	var out strings.Builder
	input := "/model\na\nsecure\n" + srv.URL + "/v1\n\nn\nq\n"
	r := New(cfg, strings.NewReader(input), &out)
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	saved, _ := config.Load(path)
	if _, ok := saved.Find("secure"); ok {
		t.Fatal("saved despite answering n")
	}
	if !strings.Contains(out.String(), "authentication failed (401 Unauthorized). This server needs an API key") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestExplainErr(t *testing.T) {
	t.Setenv("SET_KEY", "x")
	p := func(env string) config.Provider {
		return config.Provider{Name: "p", BaseURL: "http://host:1234/v1", APIKeyEnv: env}
	}
	status := func(code int) error {
		return &llm.HTTPError{Method: "GET", URL: "http://host:1234/v1/models", StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)), Body: "nope"}
	}
	tests := []struct {
		name string
		err  error
		p    config.Provider
		want string
	}{
		{"401 no key", status(401), p(""), "needs an API key"},
		{"401 unset", status(401), p("UNSET_KEY_FOR_TEST"), "$UNSET_KEY_FOR_TEST is not set"},
		{"401 set", status(401), p("SET_KEY"), "Check that $SET_KEY holds a valid key"},
		{"401 invalid", status(403), p(fakeSecret), "key setting is invalid"},
		{"404", status(404), p(""), "endpoint not found (404 for http://host:1234/v1/models)"},
		{"500", status(500), p(""), "returned 500 Internal Server Error: nope"},
		{"refused", &url.Error{Op: "Get", URL: "u", Err: &net.OpError{Op: "dial", Err: fmt.Errorf("connect: %w", errConnRefused())}}, p(""), "nothing is listening at host:1234"},
		{"dns", &url.Error{Op: "Get", URL: "u", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Name: "host", IsNotFound: true}}}, p(""), "can't find the host host:1234"},
		{"timeout", fmt.Errorf("get: %w", context.DeadlineExceeded), p(""), "no response from host:1234"},
		{"other", errors.New("weird"), p(""), "weird"},
	}
	for _, tt := range tests {
		got := explainErr(tt.err, tt.p)
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s: %q, want containing %q", tt.name, got, tt.want)
		}
		if strings.Contains(got, fakeSecret) {
			t.Errorf("%s: printed the key setting", tt.name)
		}
	}
}

func errConnRefused() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := l.Addr().String()
	l.Close()
	_, err = net.Dial("tcp", addr)
	return err
}

func TestLooksLikeSecret(t *testing.T) {
	for s, want := range map[string]bool{
		fakeSecret:                         true,
		"sk-proj-abc123def456ghi789":       true,
		"OPENAI_API_KEY":                   false,
		"AZURE_AI_FOUNDRY_ANTHROPIC_KEY_2": false,
		"my_key":                           false,
	} {
		if got := looksLikeSecret(s); got != want {
			t.Errorf("looksLikeSecret(%q) = %v", s, got)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		in, want, note string
	}{
		{"http://localhost:11434/v1/", "http://localhost:11434/v1", ""},
		{" https://api.example.com/v1/models ", "https://api.example.com/v1", "removed /models"},
		{"http://localhost:8000", "http://localhost:8000", ""},
		{"https://x.services.ai.azure.com/anthropic/v1/messages", "https://x.services.ai.azure.com/anthropic/v1", "removed /messages"},
	}
	for _, tt := range tests {
		got, notes, err := normalizeBaseURL(tt.in)
		if err != nil || got != tt.want || (tt.note != "" && !strings.Contains(strings.Join(notes, "\n"), tt.note)) {
			t.Errorf("normalizeBaseURL(%q) = %q, %v, %v", tt.in, got, notes, err)
		}
	}
	for _, bad := range []string{"localhost:11434", "ftp://x/v1", "not a url"} {
		if _, _, err := normalizeBaseURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// azureLike mimics Claude on Azure AI Foundry: the Anthropic API under
// /anthropic, and no model listing.
func azureLike(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/messages" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello from claude\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAddAnthropicProviderEndToEnd(t *testing.T) {
	srv := azureLike(t)
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := config.Load(path)
	var out strings.Builder
	input := "/model\na\nazure\n" + srv.URL + "/anthropic/v1/messages\n\nclaude-sonnet-5\nhi\n"
	r := New(cfg, strings.NewReader(input), &out)
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	o := out.String()
	for _, want := range []string{
		"removed /messages",
		"✓ found (Anthropic API). This server doesn't list its models",
		"doesn't list its models. On Azure, use the deployment name",
		"tools: supported",
		"hello from claude",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("output missing %q", want)
		}
	}
	saved, _ := config.Load(path)
	p, _ := saved.Find("azure")
	if p.API != llm.APIAnthropic || p.BaseURL != srv.URL+"/anthropic/v1" || saved.Model != "claude-sonnet-5" {
		t.Fatalf("saved provider = %+v, model = %q\n%s", p, saved.Model, o)
	}
}

func TestDetectionIsSavedForOldConfigs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"m"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := config.Load(path)
	cfg.Upsert(config.Provider{Name: "old", BaseURL: srv.URL})
	cfg.Provider, cfg.Model = "old", "m"
	cfg.SetModelInfo("old", "m", config.ModelInfo{Tools: llm.ToolsSupported})
	cfg.Save()

	var out strings.Builder
	r := New(cfg, strings.NewReader("hi\n"), &out)
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	saved, _ := config.Load(path)
	p, _ := saved.Find("old")
	if p.API != llm.APICompat || p.BaseURL != srv.URL+"/v1" || !strings.Contains(out.String(), "ok") {
		t.Fatalf("provider = %+v\n%s", p, out.String())
	}
}

func TestValidateKeyInput(t *testing.T) {
	for in, wantErr := range map[string]bool{
		"":                        false,
		"-":                       false,
		fakeSecret:                false,
		"$MY_KEY":                 false,
		"$1BAD":                   true,
		"https://api.example.com": true,
		"two words":               true,
	} {
		_, err := validateKeyInput(in)
		if (err != nil) != wantErr {
			t.Errorf("validateKeyInput(%q) err = %v", in, err)
		}
		if err != nil && strings.Contains(err.Error(), in) {
			t.Errorf("error echoes the input: %v", err)
		}
	}
}

func TestPastedKeyIsErasedFromScreen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := config.Load(path)
	var out strings.Builder
	var seen []llm.Request
	var checks int
	r := New(cfg, strings.NewReader("/model\na\nwork\nhttps://x/v1\n"+fakeSecret+"\nq\n"), &out)
	r.NewProvider = func(p config.Provider) llm.Provider {
		return fakeProvider{models: []string{"m"}, seen: &seen, support: llm.ToolsSupported, checks: &checks}
	}
	r.Width = func() int { return 30 }
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	o := out.String()
	label := "API key [enter = none]: "
	wantLines := (len(label) + len(fakeSecret) + 29) / 30
	erase := strings.Repeat("\x1b[1A\x1b[2K", wantLines) + "\r" + label + "(key entered, 31 characters)"
	if !strings.Contains(o, erase) {
		t.Fatalf("key not erased over %d lines:\n%q", wantLines, o)
	}
}
