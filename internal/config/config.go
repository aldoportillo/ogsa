package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Provider struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

func (p Provider) APIKey() string {
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}

type Config struct {
	Providers []Provider `json:"providers"`
	Provider  string     `json:"provider"`
	Model     string     `json:"model"`

	path string
}

func Default() *Config {
	return &Config{
		Providers: []Provider{
			{Name: "ollama", BaseURL: "http://localhost:11434/v1"},
			{Name: "litellm", BaseURL: "http://localhost:4000/v1", APIKeyEnv: "LITELLM_API_KEY"},
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
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.path, append(b, '\n'), 0o644)
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
