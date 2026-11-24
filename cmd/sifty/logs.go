package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const (
	logEntryLimit = 200
	logBlockBytes = 64 << 10
	logReadLimit  = 8 << 20
	minMaskedKey  = 8
)

type logAttr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type logEntry struct {
	Time    string    `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Attrs   []logAttr `json:"attrs"`
}

type logsResp struct {
	Failed  []workFailed `json:"failed"`
	Entries []logEntry   `json:"entries"`
}

func heldKeys(store *config.Store, secrets map[string]string) func() []string {
	return func() []string {
		keys := slices.AppendSeq([]string(nil), maps.Values(secrets))
		for _, pc := range store.Get().Providers {
			keys = append(keys, os.Getenv(pc.KeyEnv))
		}
		return keys
	}
}

func usableKeys(keys []string) []string {
	usable := slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return len(k) < minMaskedKey })
	slices.SortFunc(usable, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	return slices.Compact(usable)
}

func keyReplacer(keys []string) *strings.Replacer {
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, k, "***")
	}
	return strings.NewReplacer(pairs...)
}

type keyMask struct {
	mu   sync.Mutex
	held func() []string
	sig  string
	rep  *strings.Replacer
}

func (m *keyMask) setKeys(held func() []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.held, m.sig, m.rep = held, "", nil
}

func (m *keyMask) mask(p []byte) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		return p
	}
	keys := usableKeys(m.held())
	if sig := strings.Join(keys, "\x00"); sig != m.sig {
		m.sig, m.rep = sig, keyReplacer(keys)
	}
	if len(keys) == 0 {
		return p
	}
	return []byte(m.rep.Replace(string(p)))
}

type maskedWriter struct {
	w io.Writer
	m *keyMask
}

func (mw maskedWriter) Write(p []byte) (int, error) {
	if _, err := mw.w.Write(mw.m.mask(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func logsHandler(database *db.DB, r *runner.Runner, now func() time.Time, logPath string, heldKeys func() []string) func(*http.Request, struct{}) (logsResp, error) {
	return func(req *http.Request, _ struct{}) (logsResp, error) {
		failed, err := failedWork(req.Context(), database, r, now)
		if err != nil {
			return logsResp{}, err
		}
		var keys []string
		if heldKeys != nil {
			keys = heldKeys()
		}
		mask := keyReplacer(usableKeys(keys)).Replace
		entries := []logEntry{}
		for _, path := range []string{logPath, logPath + ".1"} {
			if entries, err = readProblems(path, entries, mask); err != nil {
				return logsResp{}, err
			}
		}
		return logsResp{Failed: failed, Entries: entries}, nil
	}
}

func readProblems(path string, out []logEntry, mask func(string) string) ([]logEntry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, err
	}
	var carry []byte
	floor := max(0, info.Size()-logReadLimit)
	for pos := info.Size(); pos > floor && len(out) < logEntryLimit; {
		n := min(logBlockBytes, pos-floor)
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return out, err
		}
		lines := bytes.Split(append(buf, carry...), []byte("\n"))
		carry = lines[0]
		for i := len(lines) - 1; i >= min(1, int(pos)) && len(out) < logEntryLimit; i-- {
			if e, ok := parseLogLine(lines[i], mask); ok {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func parseLogLine(line []byte, mask func(string) string) (logEntry, bool) {
	if !bytes.Contains(line, []byte(`"level":"WARN"`)) && !bytes.Contains(line, []byte(`"level":"ERROR"`)) {
		return logEntry{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	if t, _ := dec.Token(); t != json.Delim('{') {
		return logEntry{}, false
	}
	e := logEntry{Attrs: []logAttr{}}
	for dec.More() {
		k, _ := dec.Token()
		key, _ := k.(string)
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return logEntry{}, false
		}
		value := string(raw)
		var text string
		if json.Unmarshal(raw, &text) == nil {
			value = text
		}
		switch key {
		case "time":
			e.Time = value
		case "level":
			e.Level = value
		case "msg":
			e.Message = mask(value)
		default:
			e.Attrs = append(e.Attrs, logAttr{Key: key, Value: mask(value)})
		}
	}
	return e, e.Level == "WARN" || e.Level == "ERROR"
}
