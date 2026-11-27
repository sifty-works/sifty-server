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
	return readTitled(t, company, "", body)
}

func readTitled(t *testing.T, company, title, body string) *analysis.Doc {
	t.Helper()
	return readSite(t, company, title, "", body)
}

func readSite(t *testing.T, company, title, location, body string) *analysis.Doc {
	t.Helper()
	body = listing.Normalize(body)
	doc := &analysis.Doc{Title: title, Company: company, Location: location, Origin: facts.Aggregator, Body: body, Original: listing.PlainText(body)}
	doc.Sections, doc.Headings = analysis.Split(doc)
	for _, w := range analysis.Workers {
		found, err := w.Extract(context.Background(), doc)
		if err != nil {
			t.Fatalf("%s: %v", w.Name(), err)
		}
		for _, f := range found {
			f.Source.Origin = doc.Origin
			doc.Facts = append(doc.Facts, f)
		}
	}
	doc.Facts = analysis.Effective(doc.Facts)
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
		{"camel-case full time", "employment_type", "FullTime", "hours full_time"},
		{"underscored part time", "employment_type", "part_time", "hours part_time"},
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

func factValues(doc *analysis.Doc, kind facts.Kind) string {
	var got []string
	for _, f := range doc.Facts {
		if f.Kind == kind {
			got = append(got, f.Value)
		}
	}
	return strings.Join(got, "; ")
}

func TestEngagementIsReadOnlyFromAStatementOfIt(t *testing.T) {
	cases := []struct{ name, title, body, want string }{
		{"an internship is its own value, not contract", "", `<p>This is a paid internship for students.</p>`, "internship"},
		{"full-time alone states hours, not employment", "", `<p>Full-time, 40 hours a week.</p>`, ""},
		{"smart contracts are no engagement", "", `<p>You will build smart contract tooling.</p>`, ""},
		{"contract negotiation is no engagement", "", `<p>Experience with contract negotiation.</p>`, ""},
		{"contract management is no engagement", "", `<p>Own contract management for our suppliers.</p>`, ""},
		{"contractors in equal-opportunity text are no engagement", "", `<p>We do not discriminate against employees or contractors.</p>`, ""},
		{"B2B SaaS describes the company", "", `<p>Acme is a B2B SaaS company.</p>`, ""},
		{"permanent residence is no employment", "", `<p>Candidates need permanent residence in the EU.</p>`, ""},
		{"interns and an internship programme in running text are no engagement", "", `<p>You will mentor our interns and manage the internship program.</p>`, ""},
		{"a negated statement is no engagement", "", `<p>This is not a contract role.</p>`, ""},
		{"a negated list of engagements is no engagement", "", `<p>Do not apply to fixed term and temporary roles.</p>`, ""},
		{"a government contractor employer is no engagement", "", `<p>Federal contractor position requires clearance.</p>`, ""},
		{"a government contractor self-identification request is no engagement", "", `<p>As a government contractor subject to the Vietnam Era Veterans Readjustment Assistance Act (VEVRAA), we request that you self-identify.</p>`, ""},
		{"a federal contractor equal-opportunity notice is no engagement", "", `<p>Acme is an equal opportunity employer and a federal contractor with contract work for the government.</p>`, ""},
		{"a type label followed by something else is no engagement", "", `<p>Type: Contract negotiation</p>`, ""},
		{"a footnote negation after a list is no engagement", "", `<ul><li>A chance to share in the company’s success.</li></ul><p>*Do not apply to fixed term and temporary roles</p><h3>YOUR MISSION</h3>`, ""},
		{"temporary work visa in an application question is no engagement", "", `<p>Will you need longer term employment authorization (temporary work visa or permanent residency)?*Select one</p>`, ""},
		{"mentoring interns is no engagement", "", `<p>You will mentor interns.</p>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := factValues(readTitled(t, "Acme", tc.title, tc.body), facts.Engagement); got != tc.want {
				t.Errorf("engagement = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlaceIsReadOnlyFromWhatTheVacancyStates(t *testing.T) {
	cases := []struct{ name, title, site, body, want string }{
		{"an application question restricts the place", "", "", `<h2>How to apply</h2><p>Are you legally authorized to work in the United States?</p>`, "United States"},
		{"a benefits note restricts the place", "", "", `<h2>Benefits</h2><ul><li>Eligibility: you must reside in Spain</li></ul>`, "Spain"},
		{"worldwide about customers is no place", "", "", `<p>Our customers worldwide love it.</p>`, ""},
		{"a two-letter word in a pipe header is no place", "", "", `<p>Acme | AI | Remote</p>`, ""},
		{"worldwide beside a country restriction is dropped", "", "", `<p>Work from anywhere in the world, but you must be authorized to work in Canada.</p>`, "Canada"},
		{"worldwide beside a time-zone restriction is dropped", "", "", `<p>Work from anywhere in the world, overlapping CET ± 3 hours.</p>`, ""},
		{"a parenthesised country before based is the place", "", "", `<p>This position is West Coast (U.S.) based and can be remote.</p>`, "U.S."},
		{"working from anywhere in a country is that country", "", "", `<p>Work from anywhere in Canada.</p>`, "Canada"},
		{"a soft overlap request is no time-zone restriction on worldwide", "", "", `<p>All roles with Acme Labs are global and remote-based. Unless otherwise stated, we ask that you try to overlap some working hours with our team's time zone.</p>`, "Worldwide"},
		{"a company-wide worldwide line does not override a country in the site location", "", "Switzerland", `<p>All roles with Acme Labs are global and remote-based. Unless otherwise stated, we ask that you try to overlap some working hours with our team's time zone.</p>`, "Switzerland"},
		{"a restriction in the text still overrides a country in the site location", "", "Switzerland", `<p>You must be based in Brazil.</p>`, "Brazil"},
		{"a restriction in the title beats a softer text phrase", "Engineer (Remote - LATAM)", "", `<p>US / nearshore time zones preferred. You must be based in the US.</p>`, "LATAM"},
		{"developers served in another region are no restriction", "", "", `<p>Communicate with external developers based in Asia pacific.</p>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := factValues(readSite(t, "Acme", tc.title, tc.site, tc.body), facts.Place); got != tc.want {
				t.Errorf("place = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlaceFieldsOfASiteSayOnlyWhatTheSiteStates(t *testing.T) {
	cases := []struct {
		name                 string
		origin               facts.Origin
		location, text       string
		wantPlace, wantModes string
	}{
		{"an aggregator's Anywhere is no place", facts.Aggregator, "Anywhere", "", "", ""},
		{"an aggregator's bare Remote is a work mode, not a place", facts.Aggregator, "Remote", "", "", "remote"},
		{"remote beside a city keeps the city as the place", facts.Aggregator, "Remote · Anywhere · Berlin", "", "Berlin", "remote"},
		{"a lowercase two-letter value is no place", facts.Aggregator, "na", "", "", ""},
		{"TELECOMMUTE is a remote statement from an aggregator too", facts.Aggregator, "TELECOMMUTE", "", "", "remote"},
		{"TELECOMMUTE with a country keeps the country", facts.Aggregator, "TELECOMMUTE · Poland", "", "Poland", "remote"},
		{"an employer's remote field is a work mode", facts.Employer, "Remote", "", "", "remote"},
		{"a placeholder is no place", facts.Employer, "LOCATION", "", "", ""},
		{"a country code becomes its country name", facts.Aggregator, "PL", "", "Poland", ""},
		{"a worldwide field beside a time-zone restriction is no place", facts.Employer, "Worldwide", "Time zone: CET (+/- 3 hours). We cannot consider other time zones.", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := &analysis.Doc{Origin: tc.origin, Location: tc.location, Original: tc.text}
			doc.Sections, doc.Headings = analysis.Split(doc)
			found, err := analysis.Workers[0].Extract(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			doc.Facts = found
			if got := factValues(doc, facts.Place); got != tc.wantPlace {
				t.Errorf("place = %q, want %q", got, tc.wantPlace)
			}
			if got := factValues(doc, facts.WorkMode); got != tc.wantModes {
				t.Errorf("modes = %q, want %q", got, tc.wantModes)
			}
		})
	}
}

func TestMoneyIsOnlyThePayOfTheRole(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"a perk amount before the salary does not hide it", `<p>A $6,000 learning budget per year; salary $90,000 - $110,000</p>`, `{"min":90000,"max":110000,"currency":"USD","period":"year"}`},
		{"a typo of a range is read as a range", `<p>$180,000-$200,00</p>`, `{"min":180000,"max":200000,"currency":"USD"}`},
		{"a perk amount with equity after a plus is no pay", `<p>$1,000 learning budget + 0.5% equity</p>`, ""},
		{"a sign-on bonus is no pay", `<p>A $1,500 sign-on bonus</p>`, ""},
		{"an equipment allowance is no pay", `<p>$1,000 equipment allowance</p>`, ""},
		{"a learning budget is no pay", `<p>A $12,000 annual learning budget</p>`, ""},
		{"a per-task payment is no pay", `<p>Pay: $12,000 per task</p>`, ""},
		{"a reversed range is no pay", `<p>Salary: $200,000-$150,000</p>`, ""},
		{"an amount under the floor of its period is no pay", `<p>Salary: $3 per hour</p>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := factValues(read(t, "Acme", tc.body), facts.Money); got != tc.want {
				t.Errorf("money = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkModeIsNotOnsiteFromALookAlike(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"in-person interviews are no onsite mode", `<p>We hold in-person interviews.</p>`, ""},
		{"in-person team events are no onsite mode", `<p>Join our in-person team events.</p>`, ""},
		{"remote days at home beside an office elsewhere are remote only", `<p>We work from home 5 days a week, with an office available in Paris.</p>`, "remote"},
		{"remote-friendly is no work mode", `<p>Remote-friendly culture</p>`, ""},
		{"a perk in the office of a city is no onsite mode", `<h2>Benefits</h2><ul><li>Lunch and dinner stipend (in-office NYC)</li></ul>`, ""},
		{"a hybrid way of working with remote freedom is hybrid", `<p>We work in a hybrid-but-flexible way (no set days in the office, work remotely when you like).</p>`, "hybrid"},
		{"a share of telecommuting is hybrid", `<p>50% Telecommuting Permitted.</p>`, "hybrid"},
		{"a business hybrid model is no work mode", `<p>Our hybrid model out-performs brokered contracts or other “compute markets”</p>`, ""},
		{"a city or remote in a pipe header is remote and onsite", `<p>Acme | Staff Engineers (Elixir) | NYC or REMOTE (US, Europe, South America) | Full-Time | $200K-$250K + equity | https://jobs.example.com/x</p>`, "remote; onsite"},
		{"a remote place restriction is remote only", `<p>Remote, US-based</p>`, "remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := factValues(read(t, "Acme", tc.body), facts.WorkMode); got != tc.want {
				t.Errorf("work mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKeyFactsAreReadOnlyFromWhatTheVacancyStates(t *testing.T) {
	cases := []struct{ name, title, body, want string }{
		{"the most years the requirement lines ask count", "", `<h2>Requirements</h2><ul><li>5+ years of backend experience</li><li>3+ years with Go</li></ul>`, "years 5"},
		{"a range asks its lower end", "", `<h2>Requirements</h2><ul><li>3-5 years of Go</li></ul>`, "years 3"},
		{"years of the company's history are no requirement", "", `<p>Acme was founded 12 years ago.</p>`, ""},
		{"years in the company section are no requirement", "", `<h2>About us</h2><p>We have 10 years of experience building robots.</p><h2>Requirements</h2><ul><li>5+ years of backend experience</li></ul>`, "years 5"},
		{"the title beats the text", "Senior Software Engineer", `<p>You will mentor our junior developers.</p>`, "seniority Senior"},
		{"a lead of lead generation is no seniority", "Lead Generation Specialist", `<p>Find new customers.</p>`, ""},
		{"a chief of staff is no seniority", "Chief of Staff", `<p>Support the founders.</p>`, ""},
		{"a stage in the investors' description is no stage", "", `<h2>Our investors</h2><p>Backed by top investors including a Series C fund.</p>`, ""},
		{"a stage a customer is at is no stage", "", `<p>Our customers include a Series A startup.</p>`, ""},
		{"a stage outside the company sections is no stage", "", `<h2>Requirements</h2><ul><li>Experience at a Series B startup</li></ul>`, ""},
		{"years of the company's history are no requirement outside the requirement sections", "", `<h2>Requirements</h2><ul><li>Go</li></ul><h2>About the role</h2><p>For over 10 years of work in robotics, we have built robots.</p>`, ""},
		{"years in the company section are not read when the requirement sections state none", "", `<h2>Requirements</h2><ul><li>Go</li></ul><h2>About us</h2><p>Our engineers bring 10+ years of experience.</p>`, ""},
		{"a customer's backing is no company stage", "", `<p>Our customers are VC-backed startups.</p>`, ""},
		{"raised without an amount is no raise", "", `<p>Acme raised awareness of the brand.</p>`, ""},
		{"the valuation is appended to the raise", "", `<p>Acme closed a $1.37B Series D, valued at $7.87B.</p>`, "company_stage Series D, $1.37B, valued at $7.87B"},
		{"revenue growth is no growth stage", "", `<p>We are growing over 100% a year and have grown 2-3x each year.</p>`, ""},
		{"revenue next to a stage is no raise", "", `<p>Acme is a Series B company with $5M ARR.</p>`, "company_stage Series B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range readTitled(t, "Acme", tc.title, tc.body).Facts {
				if f.Kind == facts.Years || f.Kind == facts.Seniority || f.Kind == facts.CompanyStage {
					got = append(got, string(f.Kind)+" "+f.Value)
				}
			}
			if joined := strings.Join(got, "; "); joined != tc.want {
				t.Errorf("key facts = %q, want %q", joined, tc.want)
			}
		})
	}
}
