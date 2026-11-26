package analysis

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sifty-works/sifty-server/internal/facts"
)

type siteFields struct{}

func (siteFields) Name() string { return "site_fields" }

func (siteFields) Version() int { return 4 }

func (siteFields) Produces() []facts.Kind {
	return []facts.Kind{facts.Money, facts.WorkMode, facts.Engagement, facts.Place, facts.TimeZone, facts.Hours}
}

func (siteFields) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	s := ReadSite(doc)
	var out []facts.Fact
	add := func(kind facts.Kind, value string) {
		span := s.Spans[kind]
		out = append(out, facts.Fact{Kind: kind, Value: value, Start: span.Start, End: span.End, Source: facts.Source{Way: facts.SiteField}})
	}
	if s.Money != nil {
		b, _ := json.Marshal(s.Money)
		add(facts.Money, string(b))
	}
	for _, m := range s.Modes {
		add(facts.WorkMode, m)
	}
	if s.Engagement != "" {
		add(facts.Engagement, s.Engagement)
	}
	if s.Hours != nil {
		b, _ := json.Marshal(s.Hours)
		add(facts.Hours, string(b))
	}
	for _, p := range s.Places {
		span, stated := s.Stated[strings.ToLower(p)]
		if stated && !recognisedPlace(p) {
			continue
		}
		out = append(out, facts.Fact{Kind: facts.Place, Value: p, Start: span.Start, End: span.End, Source: facts.Source{Way: facts.SiteField}})
	}
	if s.TimeZone != "" {
		add(facts.TimeZone, s.TimeZone)
	}
	return out, nil
}
