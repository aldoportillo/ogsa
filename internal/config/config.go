package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aldoportillo/ogsa/internal/llm"
)

type Provider struct {
	Name      string  `json:"name"`
	BaseURL   string  `json:"base_url"`
	Key       string  `json:"api_key,omitempty"`
	APIKeyEnv string  `json:"api_key_env,omitempty"`
	API       llm.API `json:"api,omitempty"`
}

func (p Provider) APIKey() string {
	if p.Key != "" {
		return p.Key
	}
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}

type Config struct {
	Providers []Provider `json:"providers"`
	Provider  string     `json:"provider"`
	Model     string     `json:"model"`
	// Models caches what we've learned about each model, keyed "provider/model".
	Models map[string]ModelInfo `json:"models,omitempty"`

	path string
}

type ModelInfo struct {
	Tools llm.ToolSupport `json:"tools,omitempty"`
}

func (c *Config) ModelInfo(provider, model string) ModelInfo {
	return c.Models[provider+"/"+model]
}

func (c *Config) SetModelInfo(provider, model string, info ModelInfo) {
	if c.Models == nil {
		c.Models = map[string]ModelInfo{}
	}
	c.Models[provider+"/"+model] = info
}

func Default() *Config {
	return &Config{
		Providers: []Provider{
			{Name: "ollama", BaseURL: "http://localhost:11434/v1", API: llm.APICompat},
		},
		Provider: "ollama",
	}
}

func Path() (string, error) {
	if p := os.Getenv("OGSA_CONFIG"); p != "" { //$OGSA_CONFIG
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME") // $XDG_CONFIG_HOME/ogsa/config.json
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ogsa", "config.json"), nil // ~/.config/ogsa/config.json
}

func Load(path string) (*Config, error) {
	c := Default()
	c.path = path
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Save() error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(c.path, 0o600)
}

func (c *Config) Active() (Provider, bool) {
	return c.Find(c.Provider)
}

func (c *Config) Find(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

func (c *Config) Upsert(p Provider) {
	for i := range c.Providers {
		if c.Providers[i].Name == p.Name {
			c.Providers[i] = p
			return
		}
	}
	c.Providers = append(c.Providers, p)
}

func (c *Config) Replace(old string, p Provider) {
	for i := range c.Providers {
		if c.Providers[i].Name != old {
			continue
		}
		c.Providers[i] = p
		if p.Name != old {
			renamed := map[string]ModelInfo{}
			for key, info := range c.Models {
				if model, ok := strings.CutPrefix(key, old+"/"); ok {
					delete(c.Models, key)
					renamed[p.Name+"/"+model] = info
				}
			}
			maps.Copy(c.Models, renamed)
			if c.Provider == old {
				c.Provider = p.Name
			}
		}
		return
	}
}

func (c *Config) Remove(name string) {
	c.Providers = slices.DeleteFunc(c.Providers, func(p Provider) bool { return p.Name == name })
	for key := range c.Models {
		if strings.HasPrefix(key, name+"/") {
			delete(c.Models, key)
		}
	}
	if c.Provider == name {
		c.Provider, c.Model = "", ""
	}
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidEnvName(s string) bool { return envName.MatchString(s) }

func (c *Config) File() string { return c.path }
