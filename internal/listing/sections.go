package listing

import (
	_ "embed"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed sections.yaml
var sectionsYAML []byte

type sectionEntry struct{ Text, Kind string }
type sectionVocab struct{ Headings []sectionEntry }

var sections = mustLoadSections()

func normHeading(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "’", "'"))
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ":"))
}

func mustLoadSections() sectionVocab {
	var v sectionVocab
	if err := yaml.Unmarshal(sectionsYAML, &v); err != nil {
		panic("listing: load sections.yaml: " + err.Error())
	}
	return v
}
