package models

import (
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/sifty-works/sifty-server/internal/config"
)

type Secrets struct {
	mu     sync.RWMutex
	path   string
	values map[string]string
}

func OpenSecrets(path string) (*Secrets, error) {
	s := &Secrets{path: path, values: map[string]string{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.values = parseSecrets(string(data))
	return s, nil
}

func parseSecrets(text string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := splitSecretLine(line); ok {
			out[k] = v
		}
	}
	return out
}

func splitSecretLine(line string) (string, string, bool) {
	k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
	k = strings.TrimSpace(k)
	return k, strings.TrimSpace(v), ok && k != ""
}

func (s *Secrets) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.values)
}

func (s *Secrets) Values() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Collect(maps.Values(s.values))
}

func (s *Secrets) Get(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[name]
}

func (s *Secrets) Set(name, value string) error {
	return s.rewrite(name, &value)
}

func (s *Secrets) Remove(name string) error {
	return s.rewrite(name, nil)
}

func (s *Secrets) rewrite(name string, value *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	data, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	out := make([]string, 0, len(lines)+1)
	placed := false
	for _, line := range lines {
		if k, _, ok := splitSecretLine(line); !ok || k != name {
			out = append(out, line)
		} else if value != nil && !placed {
			out = append(out, name+"="+*value)
			placed = true
		}
	}
	if value != nil && !placed {
		out = append(out, name+"="+*value)
	}
	text := ""
	if len(out) > 0 {
		text = strings.Join(out, "\n") + "\n"
	}
	if err := config.AtomicWrite(s.path, []byte(text)); err != nil {
		return err
	}
	s.values = parseSecrets(text)
	return nil
}
