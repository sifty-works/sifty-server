package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"gopkg.in/yaml.v3"
)

func (s *Store) Update(fn func(c *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newCfg := s.cfg
	if len(s.cfg.Sources) > 0 {
		newCfg.Sources = make([]SourceConfig, len(s.cfg.Sources))
		copy(newCfg.Sources, s.cfg.Sources)
	}
	newCfg.Weights, newCfg.Rates = maps.Clone(s.cfg.Weights), maps.Clone(s.cfg.Rates)
	newCfg.Providers = maps.Clone(s.cfg.Providers)
	newCfg.Work = maps.Clone(s.cfg.Work)
	if err := fn(&newCfg); err != nil {
		switch err.(type) {
		case apperr.Invalid, apperr.NotFound, apperr.Conflict, apperr.Busy:
			return err
		}
		return apperr.Invalid{Message: err.Error()}
	}

	if err := Validate(newCfg); err != nil {
		return apperr.Invalid{Message: err.Error()}
	}

	var rootNode *yaml.Node
	fileData, readErr := os.ReadFile(s.path)
	if readErr == nil {
		var doc yaml.Node
		if parseErr := yaml.Unmarshal(fileData, &doc); parseErr != nil {
			return fmt.Errorf("current settings file cannot be parsed: %w", parseErr)
		}
		if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
			rootNode = doc.Content[0]
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("reading settings file: %w", readErr)
	}
	if rootNode == nil {
		rootNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	} else if len(rootNode.Content) == 0 {
		rootNode.Style = 0
	}

	applyDiff(rootNode, s.cfg, newCfg)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	docNode := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{rootNode}}
	if err := enc.Encode(docNode); err != nil {
		return fmt.Errorf("encoding settings: %w", err)
	}
	_ = enc.Close()

	if readErr == nil {
		if err := writeBackup(s.path, buf.Bytes()); err != nil {
			return fmt.Errorf("writing backup: %w", err)
		}
	}

	if err := atomicWrite(s.path, buf.Bytes()); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	s.cfg = newCfg
	return nil
}

func applyDiff(root *yaml.Node, old, new Config) {
	if old.Port != new.Port {
		setScalar(root, "port", fmt.Sprintf("%d", new.Port), "!!int")
	}
	if old.Debug != new.Debug {
		setScalar(root, "debug", strconv.FormatBool(new.Debug), "!!bool")
	}
	if old.Collection != new.Collection {
		coll := ensureMapping(root, "collection")
		if old.Collection.Auto != new.Collection.Auto {
			setScalar(coll, "auto", strconv.FormatBool(new.Collection.Auto), "!!bool")
		}
		if old.Collection.Interval != new.Collection.Interval {
			setScalar(coll, "interval",
				shortDuration(time.Duration(new.Collection.Interval)), "!!str")
		}
	}
	if !slices.Equal(old.Sources, new.Sources) {
		updateSources(root, old.Sources, new.Sources)
	}
	for i, m := range [][2]map[string]float64{{old.Weights, new.Weights}, {old.Rates, new.Rates}} {
		if !maps.Equal(m[0], m[1]) {
			node := ensureMapping(root, []string{"weights", "rates"}[i])
			for _, k := range slices.Sorted(maps.Keys(m[1])) {
				if m[1][k] != m[0][k] {
					setScalar(node, k, strconv.FormatFloat(m[1][k], 'g', -1, 64), "")
				}
			}
		}
	}
	if !maps.Equal(old.Providers, new.Providers) {
		applyProviders(root, new.Providers)
	}
	if !maps.Equal(old.Work, new.Work) {
		applyWork(root, new.Work)
	}
	if old.AllowPaidBulk != new.AllowPaidBulk {
		setScalar(root, "allow_paid_bulk", strconv.FormatBool(new.AllowPaidBulk), "!!bool")
	}
	if old.DailyModelLimit != new.DailyModelLimit {
		setScalar(root, "daily_model_limit", strconv.Itoa(new.DailyModelLimit), "!!int")
	}
}

func applyProviders(root *yaml.Node, providers map[string]ProviderConfig) {
	if len(providers) == 0 {
		removeMappingKey(root, "providers")
		return
	}
	var doc yaml.Node
	doc.Encode(providers)
	setNodeValue(root, "providers", &doc)
}

func applyWork(root *yaml.Node, work WorkConfig) {
	var doc yaml.Node
	if len(work) > 0 {
		doc.Encode(work)
	}
	if len(doc.Content) == 0 {
		removeMappingKey(root, "work")
		return
	}
	setNodeValue(root, "work", &doc)
}

func setNodeValue(parent *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = value
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func setScalar(parent *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			v := parent.Content[i+1]
			v.Kind = yaml.ScalarNode
			v.Tag = tag
			v.Value = value
			v.Content = nil
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

func ensureMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			n := parent.Content[i+1]
			if n.Kind != yaml.MappingNode {
				n.Kind = yaml.MappingNode
				n.Tag = "!!map"
				n.Value = ""
				n.Content = nil
			}
			return n
		}
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		m,
	)
	return m
}

func ensureSequence(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			n := parent.Content[i+1]
			if n.Kind != yaml.SequenceNode {
				n.Kind = yaml.SequenceNode
				n.Tag = "!!seq"
				n.Value = ""
				n.Content = nil
				n.Style = yaml.FlowStyle
			}
			return n
		}
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
	return seq
}

func getMappingStr(node *yaml.Node, key string) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func removeMappingKey(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func updateSources(root *yaml.Node, old, new []SourceConfig) {
	seq := ensureSequence(root, "sources")

	nodeByURL := make(map[string]*yaml.Node, len(seq.Content))
	for _, item := range seq.Content {
		if u := getMappingStr(item, "url"); u != "" {
			nodeByURL[u] = item
		}
	}

	oldByURL := make(map[string]SourceConfig, len(old))
	for _, s := range old {
		oldByURL[s.URL] = s
	}

	newContent := make([]*yaml.Node, 0, len(new))
	for _, s := range new {
		if existing, ok := nodeByURL[s.URL]; ok {
			updateSourceNode(existing, oldByURL[s.URL], s)
			newContent = append(newContent, existing)
		} else {
			newContent = append(newContent, makeSourceNode(s))
		}
	}
	seq.Content = newContent

	if len(newContent) == 0 {
		seq.Style = yaml.FlowStyle
	} else {
		seq.Style = 0
	}
}

func updateSourceNode(node *yaml.Node, old, new SourceConfig) {
	if old.Name != new.Name {
		if new.Name == "" {
			removeMappingKey(node, "name")
		} else {
			setScalar(node, "name", new.Name, "!!str")
		}
	}
	if old.URL != new.URL {
		setScalar(node, "url", new.URL, "!!str")
	}
	if old.Enabled != new.Enabled {
		setScalar(node, "enabled", strconv.FormatBool(new.Enabled), "!!bool")
	}
}

func makeSourceNode(s SourceConfig) *yaml.Node {
	var doc yaml.Node
	doc.Encode(s)
	return &doc
}

func writeBackup(settingsPath string, data []byte) error {
	dir := filepath.Dir(settingsPath)
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}

	ts := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := filepath.Join(backupDir, "sifty-"+ts+".yaml")
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return err
	}

	return pruneBackups(backupDir, 10)
}

func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() &&
			strings.HasPrefix(e.Name(), "sifty-") &&
			strings.HasSuffix(e.Name(), ".yaml") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	for len(paths) > keep {
		os.Remove(paths[0])
		paths = paths[1:]
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sifty-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil {
		os.Remove(tmpPath)
		return werr
	}
	if cerr != nil {
		os.Remove(tmpPath)
		return cerr
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
