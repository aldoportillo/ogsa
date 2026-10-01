package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if got.Provider != "home" || got.Model != "qwen3:32b" || len(got.Providers) != 2 {
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

func TestAPIKeyPrefersStoredKey(t *testing.T) {
	t.Setenv("ENV_KEY", "from-env")
	if k := (Provider{Key: "stored", APIKeyEnv: "ENV_KEY"}).APIKey(); k != "stored" {
		t.Error("stored key should win")
	}
	if k := (Provider{APIKeyEnv: "ENV_KEY"}).APIKey(); k != "from-env" {
		t.Error("env key not read")
	}
	if k := (Provider{}).APIKey(); k != "" {
		t.Error("expected no key")
	}
}

func TestKeysLiveAndDieWithTheirProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ogsa", "config.json")
	c, _ := Load(path)
	c.Upsert(Provider{Name: "cloud", BaseURL: "https://x/v1", Key: "secret-one"})
	c.Save()
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "secret-one") {
		t.Fatal("key not saved")
	}
	info, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("file mode %v, dir mode %v", info.Mode().Perm(), dir.Mode().Perm())
	}

	c.Replace("cloud", Provider{Name: "cloud", BaseURL: "https://x/v1", Key: "secret-two"})
	c.Save()
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "secret-one") || !strings.Contains(string(raw), "secret-two") {
		t.Fatal("edit did not replace the key")
	}

	c.Remove("cloud")
	c.Save()
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "secret-two") {
		t.Fatal("removing the provider left its key in the file")
	}
}
