package analysis

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

type siteFields struct{}

func (siteFields) Name() string { return "site_fields" }

func (siteFields) Version() int { return 8 }

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
	restricted := len(s.Places) > 0 && textRestrictsPlace(doc)
	for _, p := range s.Places {
		if pl := places.Resolve(p); restricted && pl.Worldwide && len(pl.Countries) == 0 && len(pl.Cities) == 0 {
			continue
		}
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
