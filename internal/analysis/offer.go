package analysis

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/facts"
)

//go:embed offer.yaml
var offerYAML []byte

const offerSection = "section"

type Perk struct {
	Key   string `json:"key,omitempty"`
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

type offerKind struct {
	Key       string
	Label     string
	NotStated string `yaml:"notStated"`
	Keywords  []string
}

var offerKinds = func() []offerKind {
	var v struct{ Kinds []offerKind }
	if err := yaml.Unmarshal(offerYAML, &v); err != nil {
		panic("analysis: load offer.yaml: " + err.Error())
	}
	return v.Kinds
}()

type offer struct{}

func (offer) Name() string { return "offer" }

func (offer) Version() int { return 1 }

func (offer) Produces() []facts.Kind { return []facts.Kind{facts.Offer} }

func (offer) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	found := false
	var items []Line
	for _, s := range doc.Sections {
		if s.Name != SectionOffer {
			continue
		}
		found = true
		for _, l := range textLines(doc.Original[s.Start:s.End]) {
			l.Start, l.End = l.Start+s.Start, l.End+s.Start
			if !doc.isHeading(l) {
				items = append(items, l)
			}
		}
	}
	if !found {
		return nil, nil
	}
	perk := func(p Perk, l Line) facts.Fact {
		b, _ := json.Marshal(p)
		return vocabFact(facts.Offer, string(b), "", l.Start, l.End)
	}
	out := []facts.Fact{perk(Perk{Key: offerSection}, Line{})}
	taken := make([]*Line, len(offerKinds))
	values := make([]string, len(offerKinds))
	var others []Line
	for _, l := range items {
		i, keyword := matchKind(l.Text)
		if i < 0 || values[i] != "" {
			others = append(others, l)
			continue
		}
		l := l
		taken[i], values[i] = &l, offerValue(l.Text, keyword, offerKinds[i].NotStated)
	}
	for i, k := range offerKinds {
		if values[i] != "" {
			out = append(out, perk(Perk{Key: k.Key, Label: k.Label, Value: values[i]}, *taken[i]))
		}
	}
	for _, l := range others {
		out = append(out, perk(Perk{Value: l.Text}, l))
	}
	return out, nil
}

func matchKind(item string) (int, string) {
	low := strings.ToLower(item)
	for i, k := range offerKinds {
		for _, keyword := range k.Keywords {
			if strings.Contains(low, strings.ToLower(keyword)) {
				return i, keyword
			}
		}
	}
	return -1, ""
}

func offerValue(item, keyword, notStated string) string {
	kw := strings.ToLower(keyword)
	own := map[string]bool{}
	for _, w := range offerWords(kw) {
		own[w] = true
	}
	for _, w := range offerWords(strings.ReplaceAll(strings.ToLower(item), kw, " ")) {
		if !own[w] {
			return item
		}
	}
	return notStated
}

func offerWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
