package analysis_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/listing"
)

func read(t *testing.T, company, body string) *analysis.Doc {
	t.Helper()
	body = listing.Normalize(body)
	doc := &analysis.Doc{Company: company, Body: body, Original: listing.PlainText(body)}
	doc.Sections, doc.Headings = analysis.Split(doc)
	for _, w := range analysis.Workers {
		found, err := w.Extract(context.Background(), doc)
		if err != nil {
			t.Fatalf("%s: %v", w.Name(), err)
		}
		doc.Facts = append(doc.Facts, found...)
	}
	return doc
}

func TestSectionBoundaries(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"about the vacancy's company opens a company section",
			`<p>Build things.</p><h2>Requirements</h2><ul><li>5 years of Go</li></ul><h2>About Acme Robotics</h2><p>We ship robots.</p>`,
			[]string{"intro", "requirements", "company"}},
		{"a deeper unrecognised heading stays inside its parent section",
			`<h2>Requirements</h2><h3>Backend</h3><ul><li>Go</li></ul><h3>Frontend</h3><ul><li>React</li></ul>`,
			[]string{"requirements"}},
		{"bold paragraphs and lines ending in a colon are headings",
			`<p><strong>Requirements</strong></p><ul><li>Go</li></ul><p>Nice to have:</p><ul><li>Rust</li></ul>`,
			[]string{"requirements", "nice_to_have"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, s := range read(t, "Acme Robotics", tc.body).Sections {
				got = append(got, s.Name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sections = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTermsOutsideRequirementSectionsAreNotRequired(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"a term met only in the company section is not a requirement",
			`<h2>Requirements</h2><ul><li>5 years of Go</li></ul><h2>About us</h2><p>We must grow and know Kubernetes.</p>`,
			[]string{"requirement required 5 years of Go"}},
		{"a language met only in the company section is not required",
			`<h2>Requirements</h2><ul><li>Fluent German</li></ul><h2>About us</h2><p>Our founders speak fluent Mandarin.</p>`,
			[]string{"language required German", "requirement required Fluent German"}},
		{"offer and process sections give no requirements",
			`<h2>Benefits</h2><ul><li>You must love snacks</li></ul><h2>How to apply</h2><p>A CV is required.</p>`,
			nil},
		{"a plus marker and the nice-to-have section flag lines preferred",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>Rust is a plus</li></ul><h2>Nice to have</h2><ul><li>Kafka</li></ul>`,
			[]string{"requirement required Go experience", "requirement preferred Rust is a plus", "requirement preferred Kafka"}},
		{"a text without requirement headings uses its marked lines",
			`<p>We want someone with 5 years of experience in Go.</p><p>We offer a salary and benefits.</p><p>Experience with Rust is a bonus.</p>`,
			[]string{"requirement required We want someone with 5 years of experience in Go.", "requirement preferred Experience with Rust is a bonus."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range read(t, "Acme Robotics", tc.body).Facts {
				if f.Kind == facts.Requirement || f.Kind == facts.Language {
					got = append(got, string(f.Kind)+" "+string(f.Flag)+" "+f.Value)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("facts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSiteFieldsAreReadFromTheFieldsTheSiteStates(t *testing.T) {
	cases := []struct {
		name, field, value, want string
	}{
		{"camel-case full time", "employment_type", "FullTime", "engagement employment; hours full_time"},
		{"underscored part time", "employment_type", "part_time", "engagement employment; hours part_time"},
		{"upper-case contractor", "employment_type", "CONTRACTOR", "engagement contract"},
		{"freelance", "employment_type", "Freelance", "engagement contract"},
		{"an intern word inside a longer word is no engagement", "employment_type", "International team", ""},
		{"onsite or remote offers both modes", "workplace_type", "ONSITE_OR_REMOTE", "mode remote; mode onsite"},
		{"a K suffix on the top of a range", "salary", "$160-230K", "money 160000-230000 USD"},
		{"a K suffix on both ends", "salary", "$225K – $300K", "money 225000-300000 USD"},
		{"a currency code and a stated year", "salary", "JPY 8,000,000 per year", "money 0-8000000 JPY year"},
		{"a yearly word after the code", "salary", "USD 100000 yearly", "money 0-100000 USD year"},
		{"a hyphenated yearly word", "salary", "180000-220000 USD per-year-salary", "money 180000-220000 USD year"},
		{"hours per week are not an hourly pay", "salary", "$120k, 40 hours a week", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := &analysis.Doc{Fields: map[string]string{tc.field: tc.value}}
			doc.Sections, doc.Headings = analysis.Split(doc)
			found, err := analysis.Workers[0].Extract(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, f := range found {
				switch f.Kind {
				case facts.Money:
					var m analysis.Money
					json.Unmarshal([]byte(f.Value), &m)
					got = append(got, strings.TrimSpace(fmt.Sprintf("money %.0f-%.0f %s %s", m.Min, m.Max, m.Currency, m.Period)))
				case facts.Hours:
					var h analysis.Hours
					json.Unmarshal([]byte(f.Value), &h)
					got = append(got, "hours "+h.Mode)
				case facts.WorkMode:
					got = append(got, "mode "+f.Value)
				case facts.Engagement:
					got = append(got, "engagement "+f.Value)
				}
			}
			sort.Strings(got)
			want := strings.Split(tc.want, "; ")
			if tc.want == "" {
				want = nil
			}
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("facts = %q, want %q", got, want)
			}
		})
	}
}

func TestOfferSectionsGiveOneTilePerKindAndTheOtherItems(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"one tile per kind in the vocabulary's order",
			`<h3>Benefits</h3><ul><li>Flexible hours: a four-day week</li><li>Signing bonus of 5,000</li><li>Equity: 0.5%</li></ul>`,
			"equity=Equity: 0.5%; bonus=Signing bonus of 5,000; flexible=Flexible hours: a four-day week"},
		{"a benefit named without a value takes the not stated text",
			`<h2>Perks</h2><ul><li>Equity</li><li>401(k)</li></ul>`,
			"equity=Amount not stated; retirement=Plan not stated"},
		{"a second item of a taken kind goes to the other items",
			`<h2>Benefits</h2><ul><li>Private medical cover</li><li>Dental plan for your family</li></ul>`,
			"health=Private medical cover; others: Dental plan for your family"},
		{"items matching no kind keep their order",
			`<h3>What we offer</h3><ul><li>Free lunch on Fridays</li><li>A dog in every room</li></ul>`,
			"others: Free lunch on Fridays | A dog in every room"},
		{"a line break inside a paragraph starts another item",
			`<h3>Salary</h3><p>Yearly bonus<br>Free lunch</p>`,
			"bonus=Yearly bonus; others: Free lunch"},
		{"a heading that only contains an offer word opens a section",
			`<h2>Compensation and benefits</h2><ul><li>Stock options</li></ul>`,
			"equity=Amount not stated"},
		{"the section runs to the next heading",
			`<h3>Benefits</h3><ul><li>Equity</li></ul><h3>About us</h3><ul><li>Free snacks</li></ul>`,
			"equity=Amount not stated"},
		{"text without an offer heading opens no section",
			`<h3>About the role</h3><p>Equity</p>`, "no section"},
		{"text that was never fetched opens no section", ``, "no section"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tiles, others []string
			found := false
			for _, f := range read(t, "Acme", tc.body).Facts {
				var p analysis.Perk
				if f.Kind != facts.Offer || json.Unmarshal([]byte(f.Value), &p) != nil {
					continue
				}
				switch {
				case p.Key == "section":
					found = true
				case p.Key != "":
					tiles = append(tiles, p.Key+"="+p.Value)
				default:
					others = append(others, p.Value)
				}
			}
			var parts []string
			if !found {
				parts = append(parts, "no section")
			}
			parts = append(parts, tiles...)
			if len(others) > 0 {
				parts = append(parts, "others: "+strings.Join(others, " | "))
			}
			if got := strings.Join(parts, "; "); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
