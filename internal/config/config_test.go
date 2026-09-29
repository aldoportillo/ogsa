package config

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := c.Active(); !ok || p.Name != "ollama" {
		t.Fatalf("active = %+v, %v", p, ok)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	c, _ := Load(path)
	c.Upsert(Provider{Name: "home", BaseURL: "https://llm.home/v1", APIKeyEnv: "HOME_KEY"})
	c.Upsert(Provider{Name: "ollama", BaseURL: "http://gpu-box:11434/v1"})
	c.Provider, c.Model = "home", "qwen3:32b"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "home" || got.Model != "qwen3:32b" || len(got.Providers) != 3 {
		t.Fatalf("got %+v", got)
	}
	if p, _ := got.Find("ollama"); p.BaseURL != "http://gpu-box:11434/v1" {
		t.Fatalf("upsert did not replace: %+v", p)
	}
	t.Setenv("HOME_KEY", "secret")
	if p, _ := got.Active(); p.APIKey() != "secret" {
		t.Fatalf("APIKey = %q", p.APIKey())
	}
}
