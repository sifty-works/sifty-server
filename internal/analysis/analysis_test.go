package analysis_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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
	doc := &analysis.Doc{Title: title, Company: company, Location: location, Origin: facts.Aggregator, Body: body, Original: listing.PlainText(body), FirstSeen: time.Now()}
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
			`<h2>Requirements</h2><h3>Backend</h3><ul><li>Go experience</li></ul><h3>Frontend</h3><ul><li>React</li></ul>`,
			[]string{"requirements"}},
		{"bold paragraphs and lines ending in a colon are headings",
			`<p><strong>Requirements</strong></p><ul><li>Go experience</li></ul><p>Nice to have:</p><ul><li>Rust</li></ul>`,
			[]string{"requirements", "nice_to_have"}},
		{"a heading glued to the start of its line opens its section",
			`<p>Bring a laptop.</p><p>BENEFITS: Lunch and a gym</p>`,
			[]string{"intro", "offer"}},
		{"a short unmarked line with a section phrase and no list or run of paragraphs after it opens no section",
			`<p>Build things.</p><p>Why we are a good fit</p><p>Closing remark.</p>`,
			[]string{"intro"}},
		{"duty sub-headings that hold a requirements word open no section",
			`<p>Build things.</p><p>Partner Discovery &amp; Requirements Analysis</p><p>You talk to partners every day.</p><p>Ticket Creation &amp; Scoping</p><p>You cut work into tickets.</p>`,
			[]string{"intro"}},
		{"a label in the middle of a sentence opens no section",
			`<p>Our Product Education: we teach robots to read.</p><p>Acme sells it.</p>`,
			[]string{"intro"}},
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
		{"a language that is not required is no requirement while the working language stays",
			`<p>No German required; English is the working language.</p>`,
			[]string{"language required English"}},
		{"a language lesson in the benefits section is no language label",
			`<h2>Benefits</h2><ul><li>Language lessons: fluent German, Spanish</li></ul>`,
			nil},
		{"a clause that follows a required language has its own level",
			`<h2>Requirements</h2><ul><li>German required, French is a plus</li></ul>`,
			[]string{"language preferred French", "language required German", "requirement required German required, French is a plus"}},
		{"offer and process sections give no requirements",
			`<h2>Benefits</h2><ul><li>You must love snacks</li></ul><h2>How to apply</h2><p>A CV is required.</p>`,
			nil},
		{"a plus marker and the nice-to-have section flag lines preferred",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>Rust is a plus</li></ul><h2>Nice to have</h2><ul><li>Kafka</li></ul>`,
			[]string{"requirement required Go experience", "requirement preferred Rust is a plus", "requirement preferred Kafka"}},
		{"a text without requirement headings uses its marked lines",
			`<p>We want someone with 5 years of experience in Go.</p><p>We offer a salary and benefits.</p><p>Experience with Rust is a bonus.</p>`,
			[]string{"requirement required We want someone with 5 years of experience in Go.", "requirement preferred Experience with Rust is a bonus."}},
		{"without a requirements heading only the pieces that ask for something count, and never those about the company",
			`<p>Money is moving onto the internet. Acme Robotics spent ten years building it. Join a remote-first team.</p><p>5+ years of experience with React.</p><p>This paradigm requires a range of backgrounds and experience. We welcome the range of perspectives.</p>`,
			[]string{"requirement required 5+ years of experience with React."}},
		{"perks after a glued heading are no requirements",
			`<h2>Requirements</h2><ul><li>5 years of Go</li></ul><p>Apply with your CV.Benefits &amp; Perks;</p><ul><li>Free lunch</li></ul>`,
			[]string{"requirement required 5 years of Go"}},
		{"a heading glued to its first line leaves that line as a requirement",
			`<p>Minimum QualificationsBachelor's degree in CS</p>`,
			[]string{"requirement required Bachelor's degree in CS"}},
		{"prose after the last list of a section is no requirement",
			`<h2>Requirements</h2><ul><li>Go experience</li></ul><h2>Nice to have</h2><ul><li>Kafka</li></ul><p>We value curiosity and teamwork.</p><p>You will grow fast with us.</p>`,
			[]string{"requirement required Go experience", "requirement preferred Kafka"}},
		{"a pay or equity list item is no requirement",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>Competitive salary and equity</li></ul>`,
			[]string{"requirement required Go experience"}},
		{"pay and benefit list items are no requirements but healthcare experience stays",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>Unlimited PTO</li><li>Denver, Colorado Base Salary: $170,000 – $250,000</li><li>Health &amp; dental benefits.</li><li>Fertility and family planning support</li><li>Total compensation includes base salary and RSUs.</li><li>Familiarity with the healthcare industry, insurance, finance, and compliance</li></ul>`,
			[]string{"requirement required Go experience", "requirement required Familiarity with the healthcare industry, insurance, finance, and compliance"}},
		{"a single word that names a section is no heading inside a sentence",
			`<p>We help banks meet regulatory requirements in 30 countries. Our team is small.</p>`,
			nil},
		{"a hashtag line is no requirement",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>#LI-Remote</li></ul>`,
			[]string{"requirement required Go experience"}},
		{"paragraphs after an inline label count only when they read like a requirement",
			`<p>Requirements: strong Python engineering, power user of agentic coding.</p><p>Bonus points: AWS deployment &amp; infrastructure.</p><p>What's guaranteed: intense pace and real ownership.</p><p>Autodidacts are welcome.</p><p>Two weeks maximum from apply to reply.</p>`,
			[]string{"requirement required strong Python engineering, power user of agentic coding.", "requirement preferred AWS deployment & infrastructure."}},
		{"a no-section text keeps a line that asks for something and drops product prose that only names skills",
			`<p>The platform runs on Kubernetes and Rust.</p><p>5+ years of Go required.</p>`,
			[]string{"requirement required 5+ years of Go required."}},
		{"a paragraph after an inline label that names a known skill counts while closing remarks drop",
			`<p>Requirements: 5 years of Go.</p><p>Strong Kubernetes and Rust.</p><p>Autodidacts are welcome.</p><p>Two weeks maximum from apply to reply.</p>`,
			[]string{"requirement required 5 years of Go.", "requirement required Strong Kubernetes and Rust."}},
		{"an equal-opportunity list item is no requirement",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>We are an equal opportunity employer</li></ul>`,
			[]string{"requirement required Go experience"}},
		{"paragraphs under an unknown heading without a list are no requirements while a sub-heading above a list stays in the section",
			`<h2>Requirements</h2><ul><li>Go experience</li></ul><p><strong>How we measure success</strong></p><p>You will ship your first feature in a month.</p><p>Everyone here owns a part of the product.</p><p><strong>Systems Design</strong></p><ul><li>Design distributed systems</li></ul>`,
			[]string{"requirement required Go experience", "requirement required Design distributed systems"}},
		{"a heading-like paragraph ends the run of requirement paragraphs while short real items stay",
			`<p>Requirements</p><p>5+ years of accounting experience.</p><p>Webcam</p><p>Microphone</p><p>How we measure success</p><p>First 90 days</p><p>You have been through the product end to end.</p><p>Nice to have</p><p>Experience with Spark.</p><p>Our promise</p><p>Everyone at Acme Robotics is powered by people from around the world who care about what they build and how they build it, and we think that matters a lot for everyone.</p>`,
			[]string{"requirement required 5+ years of accounting experience.", "requirement required Webcam", "requirement required Microphone", "requirement preferred Experience with Spark."}},
		{"a company paragraph without a demand word or an eligibility word is no requirement before any heading-like paragraph",
			`<h2>Requirements</h2><p>5 years of Go.</p><p>We are a small team of engineers who ship every day.</p><p>Acme Robotics is a remote-first company and employment eligibility is contingent upon residing in an approved hiring location.</p><p>Strong Kubernetes and Rust.</p>`,
			[]string{"requirement required 5 years of Go.", "requirement required Acme Robotics is a remote-first company and employment eligibility is contingent upon residing in an approved hiring location.", "requirement required Strong Kubernetes and Rust."}},
		{"a company line without a demand word is no requirement in prose after the list but stays as a list item",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>We use Go and Kubernetes daily</li></ul><p>Our engineers have years of experience in robotics.</p><p>We expect experience with Rust.</p>`,
			[]string{"requirement required Go experience", "requirement required We use Go and Kubernetes daily", "requirement required We expect experience with Rust."}},
		{"a nice-to-have heading that also holds a requirements word keeps its section and its words are no requirement",
			`<p><strong>Requirements</strong></p><ul><li>Go experience</li></ul><p><strong>Bonus Qualifications</strong></p><ul><li>Rust experience</li></ul>`,
			[]string{"requirement required Go experience", "requirement preferred Rust experience"}},
		{"a heading that starts with a known heading word is one heading and its other words are no requirement",
			`<p>Skills You Should Juggle</p><ul><li>Go experience</li></ul>`,
			[]string{"requirement required Go experience"}},
		{"scam warnings and official-channel notes are no requirements while fraud experience stays",
			`<h2>Requirements</h2><ul><li>Go experience</li><li>Candidates are contacted only through official channels</li><li>Beware of recruitment scams</li><li>3+ years in fraud prevention</li></ul>`,
			[]string{"requirement required Go experience", "requirement required 3+ years in fraud prevention"}},
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
		{"a company name that holds a city word under the title is no place", "Operations Analyst", "", `<p>Operations Analyst</p><p>Berlin and Partners</p><p>Apply</p>`, ""},
		{"a headquarters place gives way to a restriction elsewhere in the text", "", "", `<p>Headquarters: Remote, US</p><p>You must be based in Poland.</p>`, "Poland"},
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
		{"a bare Location heading before a sentence is no place", facts.Employer, "", "Location\nOur offices are in Lisbon, Portugal and Porto.", "", ""},
		{"a country label lists one place per item without the word only", facts.Employer, "", "Country\nCanada Only, Poland, and Bulgaria", "Canada; Poland; Bulgaria", ""},
		{"a listing's own location list is one place per item", facts.Aggregator, "Chicago, Seattle, Denver", "", "Chicago; Seattle; Denver", ""},
		{"a state code after an unknown city is never a country", facts.Aggregator, "Pinewood, CA", "", "Pinewood, CA", ""},
		{"a city that is also a country does not join the next place", facts.Aggregator, "Singapore, United States of America", "", "Singapore; United States of America", ""},
		{"a mode word in parentheses leaves just the place", facts.Aggregator, "Remote (US only)", "", "United States", "remote"},
		{"an item about time zones is no place", facts.Aggregator, "PT to ET overlap preferred", "", "", ""},
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
		{"hybrid financing in company prose is no work mode", `<p>We provide structured or hybrid financing to growing firms.</p>`, ""},
		{"days in the office with a plus sign are hybrid only", `<p>HYBRID, 2+ days in office</p>`, "hybrid"},
		{"in person at an office with remote participation ruled out is onsite only", `<p>This is in person at the Boston or Denver office; remote participation cannot be accommodated.</p>`, "onsite"},
		{"a negated demand before the office is no onsite mode", `<p>For our hybrid roles, we're flexible — we don't expect or mandate that you're in the office every day.</p>`, "hybrid"},
		{"a negation in another sentence does not blank an office requirement", `<p>We do not expect overtime. You will need to be in the office every day.</p>`, "onsite"},
		{"a headquarters line gives way to a statement in any part of the text", `<p>Headquarters: Remote</p><h2>About us</h2><p>We believe in having our employees onsite three days a week.</p>`, "hybrid"},
		{"an office phrase in a sentence that says hybrid adds no onsite mode", `<p>Hybrid model: most of us work in the office on Tuesdays.</p>`, "hybrid"},
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
		{"years of the company's history are no requirement outside the requirement sections", "", `<h2>Requirements</h2><ul><li>Go experience</li></ul><h2>About the role</h2><p>For over 10 years of work in robotics, we have built robots.</p>`, ""},
		{"years in the company section are not read when the requirement sections state none", "", `<h2>Requirements</h2><ul><li>Go experience</li></ul><h2>About us</h2><p>Our engineers bring 10+ years of experience.</p>`, ""},
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

func TestConditionFactsAreReadFromWhatTheVacancyStates(t *testing.T) {
	soon, past := time.Now().AddDate(0, 0, 10), time.Now().AddDate(0, 0, -10)
	cases := []struct{ name, body, want string }{
		{"a year-less date ahead takes the nearest year", "<p>Apply by " + soon.Format("2 January") + "</p>", "deadline " + soon.Format("2006-01-02")},
		{"a year-less date just passed stays in the past", "<p>Apply by " + past.Format("2 January") + "</p>", "deadline " + past.Format("2006-01-02")},
		{"an ISO date is read as written", "<p>Applications close on 2026-12-01</p>", "deadline 2026-12-01"},
		{"a start date and an interview date are no deadline", "<p>Start date: 15 January 2027. Interviews on 5 December 2026.</p>", ""},
		{"the date after the cue is the deadline, not a later start", "<p>Apply by 1 December 2026; the role starts on 15 January 2027.</p>", "deadline 2026-12-01"},
		{"a date that does not exist is no deadline", "<p>Apply by 31 February 2026</p>", ""},
		{"a six-month contract is six months", "<p>A 6-month contract</p>", "contract_length 6 months"},
		{"a one-year fixed term stays one year", "<p>A 1-year fixed-term contract</p>", "contract_length 1 year"},
		{"a spelled-out number of months is read", "<p>A seven-month contract</p>", "contract_length 7 months"},
		{"a contract followed by a number of months with a plus is read", "<p>Contract, 6+ months</p>", "contract_length 6 months"},
		{"a range of months is shown as a range", "<p>A 3-6 month contract</p>", "contract_length 3–6 months"},
		{"weeks are shown as weeks", "<p>Duration: 16 weeks</p>", "contract_length 16 weeks"},
		{"a commitment range keeps both ends", "<p>Commitment of 4 to 8 months.</p>", "contract_length 4–8 months"},
		{"a probation period is no contract length", "<p>Permanent contract, 6 months probation.</p>", ""},
		{"full-time before experience is no hours", "<p>3+ years of full-time experience in accounting.</p>", ""},
		{"part-time in a benefits list is no hours", "<h2>Benefits</h2><ul><li>Parental leave with a gradual return part-time.</li></ul>", ""},
		{"a smart contract has no length", "<p>We audit a smart contract for 3 months.</p>", ""},
		{"contract management experience is no contract length", "<p>Minimum 6 months contract management experience</p>", ""},
		{"contracts our customers sign are no contract length", "<p>Our customers sign 12-month contracts.</p>", ""},
		{"a citizenship in an equal-opportunity paragraph is no clearance", "<p>Acme is an equal opportunity employer; employees on federal work must be US citizens or permanent residents.</p>", ""},
		{"a denied clearance is no clearance", "<p>No security clearance required.</p>", ""},
		{"building public trust is no public trust position", "<p>We work to build public trust with our customers.</p>", ""},
		{"customs clearance is no clearance", "<p>We ship customs clearance software and run a clearance sale.</p>", ""},
		{"a relocation bonus is relocation offered", "<p>We pay a relocation bonus.</p>", "relocation offered"},
		{"no relocation support is none offered", "<p>There is no relocation support.</p>", "relocation none"},
		{"a denial before a but does not reach what follows", "<p>Visa sponsorship is not available, but we offer relocation assistance.</p>", "relocation offered; visa refused"},
		{"relocation in a customer's business is no relocation offer", "<p>We build software for relocation companies; our customers manage relocation assistance for families.</p>", ""},
		{"relocation that is not available is none offered", "<p>Relocation not available.</p>", "relocation none"},
		{"relocation that is not required is no relocation offer", "<p>Relocation is not required.</p>", ""},
		{"a question about sponsorship is no visa statement", "<p>Do you need, or will you need in the future, sponsorship for a work visa?</p>", ""},
		{"a statement the applicant is asked to type is no visa statement", "<p>Please type Yes to confirm that you require visa sponsorship.</p>", ""},
		{"not requiring sponsorship now or later is visa refused", "<p>Applicants who will not now or in the future require work visa sponsorship are welcome.</p>", "visa refused"},
		{"refusing sponsorship including a transfer is visa refused", "<p>Acme does not offer visa sponsorship, including H-1B transfers.</p>", "visa refused"},
		{"supported visa transfers are visa offered", "<p>Visa transfers supported.</p>", "visa offered"},
		{"sponsoring visas is visa offered", "<p>We sponsor visas.</p>", "visa offered"},
		{"unable to sponsor is visa refused", "<p>We are unable to sponsor.</p>", "visa refused"},
		{"visa sponsorship that is not available is visa refused", "<p>Visa sponsorship is not available.</p>", "visa refused"},
		{"sponsoring events is no visa", "<p>We sponsor open source projects and events.</p>", ""},
	}
	kinds := map[facts.Kind]bool{facts.Deadline: true, facts.ContractLen: true, facts.Clearance: true, facts.Relocation: true, facts.Visa: true, facts.Hours: true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range read(t, "Acme", tc.body).Facts {
				if kinds[f.Kind] {
					got = append(got, string(f.Kind)+" "+f.Value)
				}
			}
			if joined := strings.Join(got, "; "); joined != tc.want {
				t.Errorf("condition facts = %q, want %q", joined, tc.want)
			}
		})
	}
}
