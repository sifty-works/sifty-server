package analysis_test

import (
	"context"
	"reflect"
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
