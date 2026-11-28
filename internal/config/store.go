package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type OpenResult struct {
	Broken  string
	Err     string
	Backup  string
	SaveErr error
	Unknown []string
}

type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func Open(path string) (*Store, OpenResult, error) {
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		return &Store{path: path, cfg: Defaults()}, OpenResult{}, nil
	}
	if readErr != nil {
		return nil, OpenResult{}, fmt.Errorf("reading settings file: %w", readErr)
	}
	cfg, unknown, err := parse(data)
	if err == nil {
		return &Store{path: path, cfg: cfg}, OpenResult{Unknown: unknown}, nil
	}
	result := OpenResult{Err: err.Error()}
	var invalid notSettingsFile
	if errors.As(err, &invalid) {
		result.Err = NotSettingsFileSentence
	}
	moved := false
	if b := path + ".broken-" + time.Now().UTC().Format("20060102T150405"); os.Rename(path, b) == nil {
		result.Broken = filepath.Base(b)
		moved = true
	}
	cfg, data, backup := restoreSettings(path)
	result.Backup = backup
	if moved {
		result.SaveErr = AtomicWrite(path, data)
	}
	return &Store{path: path, cfg: cfg}, result, nil
}

func restoreSettings(settingsPath string) (Config, []byte, string) {
	dataDir := filepath.Dir(settingsPath)
	entries, _ := os.ReadDir(filepath.Join(dataDir, "backups"))
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "sifty-") && strings.HasSuffix(e.Name(), ".yaml") {
			paths = append(paths, filepath.Join(dataDir, "backups", e.Name()))
		}
	}
	sort.Strings(paths)
	for i := len(paths) - 1; i >= 0; i-- {
		if d, err := os.ReadFile(paths[i]); err == nil {
			if cfg, _, err := parse(d); err == nil {
				return cfg, d, filepath.Base(paths[i])
			}
		}
	}
	return Defaults(), []byte(DefaultsYAML()), ""
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}
