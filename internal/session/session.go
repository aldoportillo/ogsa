package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aldoportillo/ogsa/internal/llm"
)

// Store holds one project's sessions.
type Store struct{ Dir string }

var unsafe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// ProjectKey names a project's folder: readable, plus a short hash so
// different paths that sanitize alike don't collide.
func ProjectKey(projectDir string) string {
	sum := sha256.Sum256([]byte(projectDir))
	name := strings.Trim(unsafe.ReplaceAllString(filepath.Base(projectDir), "-"), "-")
	return name + "-" + hex.EncodeToString(sum[:4])
}

func ForProject(configDir, projectDir string) *Store {
	return &Store{Dir: filepath.Join(configDir, "projects", ProjectKey(projectDir), "sessions")}
}

func (s *Store) New() *Session { return &Session{store: s} }

func (s *Store) Open(path string) *Session { return &Session{store: s, path: path} }

type Session struct {
	store *Store
	path  string
}

func (s *Session) Path() string { return s.path }

func (s *Session) Append(msgs ...llm.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.path == "" {
		if err := s.create(); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

func (s *Session) create() error {
	if err := os.MkdirAll(s.store.Dir, 0o700); err != nil {
		return err
	}
	base := time.Now().Format("2006-01-02_15-04-05")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		path := filepath.Join(s.store.Dir, name+".jsonl")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		s.path = path
		return f.Close()
	}
}

type Info struct {
	Path        string
	Modified    time.Time
	Messages    int
	FirstPrompt string
}

func (s *Store) List() ([]Info, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var infos []Info
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(s.Dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		msgs, err := Load(path)
		if err != nil || len(msgs) == 0 {
			continue
		}
		info := Info{Path: path, Modified: fi.ModTime(), Messages: len(msgs)}
		if i := slices.IndexFunc(msgs, func(m llm.Message) bool { return m.Role == llm.RoleUser }); i >= 0 {
			info.FirstPrompt = msgs[i].Content
		}
		infos = append(infos, info)
	}
	slices.SortFunc(infos, func(a, b Info) int { return b.Modified.Compare(a.Modified) })
	return infos, nil
}

func Load(path string) ([]llm.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var msgs []llm.Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var m llm.Message
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Role != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, sc.Err()
}
