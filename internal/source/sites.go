package source

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/industry"
	"gopkg.in/yaml.v3"
)

//go:embed sites.yaml
var embeddedSites []byte

type knownSite struct {
	mod  Module
	site Site
}

type siteEntry struct {
	Host   string `yaml:"host"`
	Module string `yaml:"module"`
	Site   `yaml:",inline"`
}

type sitesFile struct {
	Sites []siteEntry `yaml:"sites"`
}

var knownSiteModules = map[string]Module{}

var sitesByHost map[string]knownSite

func init() {
	for _, p := range pageParsers {
		if p.name != "" {
			knownSiteModules[p.name] = &pageModule{p}
		}
	}
	entries, err := parseSites(embeddedSites)
	if err != nil {
		panic("source: cannot parse embedded sites.yaml: " + err.Error())
	}
	sitesByHost = buildSiteMap(entries)
}

func parseSites(data []byte) ([]siteEntry, error) {
	var f sitesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(f.Sites))
	for _, e := range f.Sites {
		if strings.TrimSpace(e.Host) == "" {
			return nil, fmt.Errorf("sites: host must not be empty")
		}
		if seen[e.Host] {
			return nil, fmt.Errorf("sites: duplicate host %q", e.Host)
		}
		seen[e.Host] = true
		if _, ok := knownSiteModules[e.Module]; !ok {
			return nil, fmt.Errorf("sites: unknown module %q for host %q", e.Module, e.Host)
		}
		if e.LinkPattern != "" {
			if _, err := regexp.Compile(e.LinkPattern); err != nil {
				return nil, fmt.Errorf("sites: invalid link_pattern for host %q: %w", e.Host, err)
			}
		}
		allowed := make(map[string]bool, len(industry.Names()))
		for _, n := range industry.Names() {
			allowed[n] = true
		}
		for _, ind := range e.Site.Industries {
			if !allowed[ind] {
				return nil, fmt.Errorf("sites: unknown industry %q for host %q", ind, e.Host)
			}
		}
	}
	return f.Sites, nil
}

func buildSiteMap(entries []siteEntry) map[string]knownSite {
	m := make(map[string]knownSite, len(entries))
	for _, e := range entries {
		m[e.Host] = knownSite{mod: knownSiteModules[e.Module], site: e.Site}
	}
	return m
}

func LoadSites(overridePath string) (broken string, err error) {
	data, readErr := os.ReadFile(overridePath)
	if os.IsNotExist(readErr) {
		return "", nil
	}
	if readErr != nil {
		return "", readErr
	}
	entries, parseErr := parseSites(data)
	if parseErr != nil {
		if b := overridePath + ".broken-" + time.Now().UTC().Format("20060102T150405"); os.Rename(overridePath, b) == nil {
			broken = filepath.Base(b)
		}
		return broken, parseErr
	}
	base, _ := parseSites(embeddedSites)
	sitesByHost = buildSiteMap(append(base, entries...))
	return "", nil
}
