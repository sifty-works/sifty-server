package source

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	knownSiteModules[srcBraintrust.Name()] = srcBraintrust
	entries, err := parseSites(embeddedSites)
	if err != nil {
		panic("source: cannot parse embedded sites.yaml: " + err.Error())
	}
	sitesByHost = buildSiteMap(entries)
}

type SitesRefused struct{ Reason string }

func (e SitesRefused) Error() string { return e.Reason }

func parseSites(data []byte) ([]siteEntry, error) {
	var f sitesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, SitesRefused{"It is not a valid site descriptions file."}
	}
	seen := make(map[string]bool, len(f.Sites))
	for _, e := range f.Sites {
		if strings.TrimSpace(e.Host) == "" {
			return nil, SitesRefused{"A site has no host."}
		}
		if seen[e.Host] {
			return nil, SitesRefused{fmt.Sprintf("The host %s is listed twice.", e.Host)}
		}
		seen[e.Host] = true
		if _, ok := knownSiteModules[e.Module]; !ok {
			return nil, SitesRefused{fmt.Sprintf("The host %s names an unknown module %s.", e.Host, e.Module)}
		}
		if e.LinkPattern != "" {
			if _, err := regexp.Compile(e.LinkPattern); err != nil {
				return nil, SitesRefused{fmt.Sprintf("The host %s has a link pattern that is not a valid regular expression.", e.Host)}
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
