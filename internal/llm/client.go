package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type API string

const (
	APIDetect    API = ""
	APICompat    API = "compat"    // Follows openai api specs for models
	APIAnthropic API = "anthropic" // Anthropic does their own thing. Thanks for the headache.
)

func (a API) String() string {
	switch a {
	case APICompat:
		return "OpenAI-compatible API"
	case APIAnthropic:
		return "Anthropic API"
	default:
		return "unknown API"
	}
}

type Detection struct {
	API     API
	BaseURL string
}

type dialect interface {
	Provider
	ToolChecker
}

type Client struct {
	BaseURL  string
	APIKey   string
	API      API
	HTTP     *http.Client
	OnDetect func(Detection)

	mu      sync.Mutex
	dialect dialect
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	d, err := c.get(ctx)
	if err != nil {
		return nil, err
	}
	return d.ListModels(ctx)
}

func (c *Client) Chat(ctx context.Context, r Request, onDelta func(string)) (Message, error) {
	d, err := c.get(ctx)
	if err != nil {
		return Message{}, err
	}
	return d.Chat(ctx, r, onDelta)
}

func (c *Client) CheckTools(ctx context.Context, model string) (ToolSupport, error) {
	d, err := c.get(ctx)
	if err != nil {
		return ToolsUnknown, err
	}
	return d.CheckTools(ctx, model)
}

func (c *Client) Detect(ctx context.Context) (Detection, error) {
	_, err := c.get(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	return Detection{API: c.API, BaseURL: c.BaseURL}, err
}

func (c *Client) get(ctx context.Context) (dialect, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dialect != nil {
		return c.dialect, nil
	}
	if c.API == APIDetect {
		det, err := c.detect(ctx)
		if det.API == APIDetect {
			return nil, err
		}
		c.API, c.BaseURL = det.API, det.BaseURL
		if c.OnDetect != nil {
			c.OnDetect(det)
		}
		if err != nil {
			return nil, err
		}
	}
	c.dialect = c.build(c.API, c.BaseURL)
	return c.dialect, nil
}

func (c *Client) build(api API, base string) dialect {
	if api == APIAnthropic {
		a := NewAnthropicClient(base, c.APIKey)
		a.HTTP = c.HTTP
		return a
	}
	cc := NewCompatClient(base, c.APIKey)
	if c.HTTP != nil {
		cc.HTTP = c.HTTP
	}
	return cc
}

func (c *Client) detect(ctx context.Context) (Detection, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if looksAnthropic(base) {
		return Detection{APIAnthropic, base}, nil
	}

	_, firstErr := c.build(APICompat, base).ListModels(ctx)
	if firstErr == nil {
		return Detection{APICompat, base}, nil
	}
	var httpErr *HTTPError
	if !errors.As(firstErr, &httpErr) {
		return Detection{}, firstErr // unreachable: no other format will do better
	}

	if !strings.HasSuffix(base, "/v1") {
		withV1 := base + "/v1"
		if _, err := c.build(APICompat, withV1).ListModels(ctx); err == nil {
			return Detection{APICompat, withV1}, nil
		}
	}

	_, err := c.build(APIAnthropic, base).ListModels(ctx)
	switch {
	case err == nil:
		return Detection{APIAnthropic, base}, nil
	case isAnthropicError(err):
		return Detection{APIAnthropic, base}, err // e.g. a rejected key
	}
	return Detection{}, firstErr
}

func looksAnthropic(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return u.Hostname() == "api.anthropic.com" || strings.Contains(u.Path, "/anthropic")
}

func isAnthropicError(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	var body struct {
		Type string `json:"type"`
	}
	return json.Unmarshal([]byte(httpErr.Body), &body) == nil && body.Type == "error"
}
