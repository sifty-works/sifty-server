package fit_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/fit"
)

func num(x float64) *float64 { return &x }

func rules() fit.Rules {
	d := config.Defaults()
	return fit.Rules{Weights: d.Weights, Rates: d.Rates}
}

type vacancy struct {
	Title, Company, Location string
	Tags                     []string
	Fields                   map[string]string
	Text                     string
}

func fromCode(v vacancy) fit.Vacancy {
	doc := &analysis.Doc{Title: v.Title, Company: v.Company, Location: v.Location, Tags: v.Tags, Fields: v.Fields, Original: v.Text}
	doc.Sections, doc.Headings = analysis.Split(doc)
	set := facts.Set{Analysed: true}
	for _, w := range analysis.Workers {
		found, _ := w.Extract(ctx, doc)
		doc.Facts = append(doc.Facts, found...)
		set.Facts = append(set.Facts, found...)
	}
	return fit.FromFacts(v.Title, true, set)
}

func score(p fit.ProfileView, v vacancy) fit.Fit {
	return fit.Score(p, fromCode(v), rules())
}

func criterion(t *testing.T, f fit.Fit, name string) float64 {
	t.Helper()
	for _, c := range f.Criteria {
		if c.Name == name {
			if c.Outcome == fit.Unknown || c.Outcome == fit.Skipped {
				return -1
			}
			return c.Utility
		}
	}
	t.Fatalf("criterion %s missing in %+v", name, f.Criteria)
	return 0
}

func goals(g candidate.GoalsView, auths ...candidate.WorkAuthorization) fit.ProfileView {
	return fit.ProfileView{Goals: g, Authorizations: auths}
}

var eur = &candidate.Compensation{CashMin: num(90000), Currency: "EUR", Period: "year"}

func TestEachCriterionMatchesMismatchesOrCannotBeCompared(t *testing.T) {
	cases := []struct {
		name, criterion string
		p               fit.ProfileView
		v               vacancy
		want            float64
	}{
		{"role overlap", "role", goals(candidate.GoalsView{TargetRoles: []string{"Backend Engineer"}}), vacancy{Title: "Senior Backend Engineer"}, 0.8},
		{"role disjoint", "role", goals(candidate.GoalsView{TargetRoles: []string{"Designer"}}), vacancy{Title: "Go Engineer"}, 0},
		{"role without targets", "role", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), vacancy{Title: "Go Engineer"}, -1},
		{"money range above minimum", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: €95,000 - €110,000 per year"}, 1},
		{"money top below minimum", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: EUR 60k - 72k per year"}, 0.8},
		{"top at or above the minimum", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: EUR 80k - 100k per year"}, 1},
		{"monthly sum times twelve", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: EUR 7,000 per month"}, 84000.0 / 90000},
		{"hourly sum times 2080", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: EUR 50 per hour"}, 1},
		{"converted by rate", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: USD 90,000 per year"}, 0.92},
		{"no rate for currency", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: CHF 50,000 per year", Fields: nil}, 0.6},
		{"net against gross", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: EUR 100,000 per year net"}, -1},
		{"floor only", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Text: "Salary: from EUR 100,000 per year"}, -1},
		{"source field salary", "money", goals(candidate.GoalsView{Compensation: eur}), vacancy{Fields: map[string]string{"salary": "95000–120000 EUR per year"}}, 1},
		{"preferred country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "DE"}}}), vacancy{Location: "Berlin, Germany"}, 1},
		{"preferred city elsewhere", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "DE", City: "Munich"}}}), vacancy{Location: "Berlin, Germany"}, 0},
		{"region covers country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "EU"}}}), vacancy{Location: "Lisbon, Portugal"}, 1},
		{"other country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), vacancy{Location: "Austin, Texas"}, 0},
		{"city only", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), vacancy{Location: "Springfield"}, -1},
		{"no preferred locations", "location", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), vacancy{Location: "Remote"}, -1},
		{"remote without a place cannot be compared", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), vacancy{Location: "Remote"}, -1},
		{"remote with only a time zone cannot be compared", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), vacancy{Location: "Remote", Text: "Time zone: CET (+/- 3 hours)"}, -1},
		{"unrestricted remote", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), vacancy{Location: "Remote · Anywhere"}, 1},
		{"telecommute is unrestricted remote", "remote_restriction", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), vacancy{Location: "TELECOMMUTE · Anywhere"}, 1},
		{"work mode shared", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote", "hybrid"}}), vacancy{Text: "Work mode: Hybrid"}, 1},
		{"work mode disjoint", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), vacancy{Location: "Onsite"}, 0},
		{"work mode not stated", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), vacancy{}, -1},
		{"restricted remote leaves work mode to the restriction", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), vacancy{Location: "Remote (US only) · Remote"}, -1},
		{"engagement from tag", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), vacancy{Tags: []string{"Contract"}}, 1},
		{"engagement disjoint", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), vacancy{Fields: map[string]string{"employment_type": "PERMANENT"}}, 0},
		{"internship against a contract goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), vacancy{Fields: map[string]string{"employment_type": "INTERN"}}, 0},
		{"internship against an employment goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"employment"}}), vacancy{Fields: map[string]string{"employment_type": "INTERN"}}, 0},
		{"no engagement stated cannot be compared with an employment goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"employment"}}), vacancy{Title: "Builder"}, -1},
		{"no engagement stated cannot be compared with a contract goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), vacancy{Title: "Builder"}, -1},
		{"no engagement goal leaves criterion not comparable", "engagement", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), vacancy{Title: "Builder"}, -1},
		{"hours mode", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time"}}), vacancy{Text: "Hours: part-time"}, 1},
		{"hours other mode", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time"}}), vacancy{Tags: []string{"Full-time"}}, 0},
		{"weekly range meets", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", Weekly: num(25)}}), vacancy{Text: "Hours: part-time, 20-30 hours per week"}, 1},
		{"weekly range above", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", WeeklyMax: num(15)}}), vacancy{Text: "Hours: part-time, 20 to 30 h/week"}, 0},
		{"weekly hours of a full-time goal not compared", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "full_time", Weekly: num(10)}}), vacancy{Text: "Hours: full-time, 40 hours per week"}, 1},
		{"weekly hours not stated", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", Weekly: num(25)}}), vacancy{Text: "Hours: part-time"}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := criterion(t, score(c.p, c.v), c.criterion); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("%s = %v, want %v", c.criterion, got, c.want)
			}
		})
	}
}

func TestRemoteRestrictionIsCheckedAgainstWorkAuthorizations(t *testing.T) {
	g := candidate.GoalsView{TargetRoles: []string{"Engineer"}}
	eligibleEU := candidate.WorkAuthorization{Country: "EU", Status: "eligible"}
	cases := []struct {
		name     string
		location string
		auths    []candidate.WorkAuthorization
		want     float64
	}{
		{"EU authorization covers a member state", "Remote - Portugal", []candidate.WorkAuthorization{eligibleEU}, 1},
		{"unrestricted without user geography", "Remote · Anywhere", nil, 1},
		{"remote with no place is not comparable", "Remote", nil, -1},
		{"needs a visa for the country", "Remote (US only)", []candidate.WorkAuthorization{eligibleEU, {Country: "US", Status: "need_visa"}}, 0},
		{"no authorization stated for the country", "Remote (US only)", []candidate.WorkAuthorization{eligibleEU}, -1},
		{"unknown place", "Remote - Atlantis", []candidate.WorkAuthorization{eligibleEU}, -1},
		{"not remote", "Lisbon, Portugal", []candidate.WorkAuthorization{eligibleEU}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := vacancy{Location: c.location}
			if c.name != "not remote" {
				v.Location += " · Remote"
			}
			if got := criterion(t, score(goals(g, c.auths...), v), "remote_restriction"); got != c.want {
				t.Errorf("remote_restriction = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTimeZoneFromStatedZoneThenPlaces(t *testing.T) {
	g := candidate.GoalsView{TimeZones: []string{"europe_africa"}}
	cases := []struct {
		name string
		v    vacancy
		want float64
	}{
		{"stated offset", vacancy{Text: "Time zone: UTC+2", Location: "United States"}, 1},
		{"stated label", vacancy{Text: "Timezone: PST"}, 0},
		{"label widened", vacancy{Text: "Time zone: EST ± 4 hours"}, 1},
		{"country", vacancy{Location: "Remote (Germany)"}, 1},
		{"region", vacancy{Location: "APAC"}, 0},
		{"phrase is not read by code", vacancy{Text: "Time zone: overlap with Pacific Time"}, -1},
		{"city only", vacancy{Location: "Springfield"}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := criterion(t, score(goals(g), c.v), "time_zone"); got != c.want {
				t.Errorf("time_zone = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequirementsScoreAffectsWeightedAverageWhenWeightIsNonZero(t *testing.T) {
	p := fit.ProfileView{
		Goals:        candidate.GoalsView{TargetRoles: []string{"Engineer"}},
		Skills:       []candidate.Skill{{Name: "Go"}},
		AbsentSkills: []candidate.AbsentSkill{{Name: "Kubernetes"}},
	}
	r0 := fit.Rules{Weights: map[string]float64{"role": 1}, Rates: rules().Rates}
	r1 := fit.Rules{Weights: map[string]float64{"role": 1, "requirements": 1}, Rates: rules().Rates}
	v := fit.Vacancy{
		Title:    "Engineer",
		Full:     true,
		Analysed: true,
		Requirements: []fit.Requirement{
			{Text: "Go", Level: "required"},
			{Text: "Kubernetes", Level: "nice_to_have"},
		},
	}
	noReqV := fit.Vacancy{Title: "Engineer", Full: true}
	if fit.Score(p, v, r0).Score != fit.Score(p, noReqV, r0).Score {
		t.Error("requirements weight 0: score differs with and without requirements read")
	}
	if fit.Score(p, v, r1).Score == fit.Score(p, noReqV, r1).Score {
		t.Error("requirements weight 1: score identical with and without requirements read")
	}
}

func TestNumberIsWeightedMeanOfKnownCriteriaAndSaysHowManyWereScored(t *testing.T) {
	p := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur})
	v := vacancy{Title: "Go Engineer", Text: "Salary: EUR 100,000 per year"}
	r := rules()
	t.Run("an unknown criterion is left out of the mean", func(t *testing.T) {
		f := fit.Score(p, fromCode(v), r)
		if f.State != fit.Scored || f.Score != 100 || f.ScoredOn != 2 || f.Of != 2 {
			t.Errorf("got %s %d on %d of %d, want scored 100 on 2 of 2", f.State, f.Score, f.ScoredOn, f.Of)
		}
		pw := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, WorkModes: []string{"remote"}, Compensation: eur})
		if f = fit.Score(pw, fromCode(v), r); f.Score != 100 || f.ScoredOn != 2 || f.Of != 3 {
			t.Errorf("an unknown work mode: got %d on %d of %d, want 100 on 2 of 3", f.Score, f.ScoredOn, f.Of)
		}
	})
	t.Run("a criterion of weight 0 is left out", func(t *testing.T) {
		r.Weights = map[string]float64{"role": 1, "money": 0}
		f := fit.Score(goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur}), fromCode(vacancy{Title: "Go Engineer", Text: "Salary: EUR 45,000 per year"}), r)
		if f.Score != 100 || f.ScoredOn != 1 || f.Of != 1 || f.Bucket != "Strong" {
			t.Errorf("only role weighted: got %d on %d of %d %s", f.Score, f.ScoredOn, f.Of, f.Bucket)
		}
	})
	t.Run("nothing known gives no number", func(t *testing.T) {
		pw := goals(candidate.GoalsView{WorkModes: []string{"remote"}, Hours: &candidate.Hours{Mode: "part_time"}})
		f := fit.Score(pw, fit.Vacancy{Title: "Builder", Full: true, Analysed: true}, rules())
		if f.State != fit.NoFacts || f.Score != 0 || f.ScoredOn != 0 || f.Bucket != "Not scored" || f.Broken {
			t.Errorf("got %+v, want no number scored on 0", f)
		}
	})
}

func TestBrokenMustCapsByItsSourceAndUnknownMustCostsNothing(t *testing.T) {
	inText := facts.Source{Way: facts.Vocabulary, Origin: facts.Employer}
	field := facts.Source{Way: facts.SiteField, Origin: facts.Aggregator}
	money := func(max int, src facts.Source) facts.Fact {
		return facts.Fact{Kind: facts.Money, Value: fmt.Sprintf(`{"max":%d,"currency":"EUR","period":"year"}`, max), Source: src, End: 1}
	}
	fact := func(kind facts.Kind, value string, src facts.Source) facts.Fact {
		f := facts.Fact{Kind: kind, Value: value, Source: src, Flag: facts.Required}
		if src == inText {
			f.End = 1
		}
		return f
	}
	remote := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur, WorkModes: []string{"remote"}})
	cases := []struct {
		name     string
		p        fit.ProfileView
		facts    []facts.Fact
		score    int
		broken   bool
		reason   string
		scoredOn int
	}{
		{"work mode broken by the text", remote, []facts.Fact{money(120000, inText), fact(facts.WorkMode, "onsite", inText)}, 15, true, "Onsite only — you want remote", 3},
		{"engagement mismatch from the text caps nothing", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Engagement: []string{"contract"}}),
			[]facts.Fact{fact(facts.Engagement, "employment", inText)}, 89, false, "Employment only — you want contract", 2},
		{"required language missing from My data", fit.ProfileView{Goals: candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}, Languages: []candidate.Language{{Name: "English"}}},
			[]facts.Fact{fact(facts.Language, "Mandarin", inText)}, 15, true, "Requires Mandarin — not in your languages", 1},
		{"work authorization broken by the text", fit.ProfileView{Goals: candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}, Authorizations: []candidate.WorkAuthorization{{Country: "US", Status: "need_visa"}}},
			[]facts.Fact{fact(facts.WorkMode, "remote", inText), fact(facts.Place, "US", inText)}, 15, true, "Remote only in", 2},
		{"work mode broken only by an aggregator field", remote, []facts.Fact{money(120000, inText), fact(facts.WorkMode, "onsite", field)}, 40, false, "Onsite only — you want remote", 3},
		{"unknown must costs nothing and says why", remote, []facts.Fact{money(120000, inText)}, 100, false, "Work mode not stated", 2},
		{"required language and no language in My data", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}),
			[]facts.Fact{fact(facts.Language, "Mandarin", inText)}, 100, false, "Requires Mandarin — your languages are not listed", 1},
		{"mismatch on a preferred criterion caps nothing", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur}),
			[]facts.Fact{money(45000, inText)}, 78, false, "Pays €45k a year — below your minimum of EUR 90,000 a year", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := fit.FromFacts("Go Engineer", true, facts.Set{Analysed: true, Facts: c.facts})
			f := fit.Score(c.p, v, rules())
			if f.State != fit.Scored || f.Score != c.score || f.Broken != c.broken || f.ScoredOn != c.scoredOn {
				t.Errorf("got %s %d broken %v on %d, want scored %d broken %v on %d", f.State, f.Score, f.Broken, f.ScoredOn, c.score, c.broken, c.scoredOn)
			}
			if len(f.Reasons) == 0 || !strings.HasPrefix(f.Reasons[0], c.reason) {
				t.Errorf("reasons %q, want the first to start with %q", f.Reasons, c.reason)
			}
		})
	}
}

func TestNoNumberWithoutFullTextOrGoals(t *testing.T) {
	v := fit.Vacancy{Title: "Go Engineer"}
	f := fit.Score(goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}), v, rules())
	if f.State != fit.NoText || f.Text != "Full job description is unavailable" || f.Bucket != "Not scored" {
		t.Errorf("without full text: %+v", f)
	}
	v.Full = true
	f = fit.Score(goals(candidate.GoalsView{}, candidate.WorkAuthorization{Country: "DE", Status: "eligible"}), v, rules())
	if f.State != fit.NoGoals || f.Text != "Your profile states no goals to compare" || f.Score != 0 {
		t.Errorf("without goals: %+v", f)
	}
}

func TestBucketEdges(t *testing.T) {
	for score, want := range map[int]string{54: "Weak", 55: "Partial", 74: "Partial", 75: "Strong"} {
		if got := fit.Bucket(score); got != want {
			t.Errorf("Bucket(%d) = %s, want %s", score, got, want)
		}
	}
}

func TestMoneyTileIsCompactAndKeepsItsConversionApart(t *testing.T) {
	cases := []struct {
		name             string
		money            fit.Money
		value, converted string
	}{
		{"a range of whole thousands", fit.Money{Min: 190000, Max: 225000, Currency: "USD", Period: "year"}, "$190–225k a year", "≈ €207,000"},
		{"pounds keep their symbol", fit.Money{Min: 40000, Max: 50000, Currency: "GBP", Period: "year"}, "£40–50k a year", "≈ €59,000"},
		{"a currency without a symbol keeps its code", fit.Money{Max: 120000, Currency: "CHF", Period: "year"}, "CHF 120k a year", "≈ €130,000"},
		{"yen keep their code and convert at the rate from the settings", fit.Money{Max: 8000000, Currency: "JPY", Period: "year"}, "JPY 8M a year", "≈ €50,000"},
		{"an amount that is not whole thousands gets one decimal", fit.Money{Max: 7500, Currency: "EUR", Period: "month"}, "€7.5k a month", "≈ €90,000"},
		{"a million and more is written in millions", fit.Money{Max: 1200000, Currency: "USD", Period: "year"}, "$1.2M a year", "≈ €1,104,000"},
		{"ends that differ in suffix carry one each", fit.Money{Min: 900, Max: 1200000, Currency: "EUR", Period: "year"}, "€900–1.2M a year", "≈ €1,200,000"},
		{"a sum that rounds up to a thousand thousands is written in millions", fit.Money{Max: 999950, Currency: "USD", Period: "year"}, "$1M a year", "≈ €920,000"},
		{"a sum just under that still reads in thousands", fit.Money{Max: 999949, Currency: "USD", Period: "year"}, "$999.9k a year", "≈ €920,000"},
		{"a sum that rounds up to a thousand is written in thousands", fit.Money{Max: 999.5, Currency: "EUR", Period: "day"}, "€1k a day", "≈ €260,000"},
		{"an amount below a thousand keeps its digits", fit.Money{Max: 850, Currency: "EUR", Period: "day"}, "€850 a day", "≈ €221,000"},
		{"only a minimum writes one number", fit.Money{Min: 60000, Currency: "EUR", Period: "year"}, "€60k a year", ""},
		{"only a maximum writes one number", fit.Money{Max: 80000, Currency: "EUR", Period: "year"}, "€80k a year", "≈ €80,000"},
		{"no period appends nothing", fit.Money{Max: 95000, Currency: "EUR"}, "€95k", "≈ €95,000"},
		{"an hourly rate reads with the right article", fit.Money{Min: 24, Max: 33, Currency: "USD", Period: "hour"}, "$24–33 an hour", "≈ €63,000"},
		{"a currency without a rate leaves the conversion empty", fit.Money{Min: 500000, Max: 600000, Currency: "SEK", Period: "year"}, "SEK 500–600k a year", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := fit.Vacancy{Title: "Engineer", Full: true, Money: &c.money}
			tile := fit.Score(goals(candidate.GoalsView{TargetRoles: []string{"Engineer"}}), v, rules()).Tiles[0]
			if tile.Name != "Money" || tile.Value != c.value || tile.Converted != c.converted {
				t.Errorf("money tile = %+v, want value %q and converted %q", tile, c.value, c.converted)
			}
		})
	}
}

func TestTilesNeverDisagreeWithTheCriteria(t *testing.T) {
	p := goals(candidate.GoalsView{Compensation: eur, WorkModes: []string{"onsite"}, Engagement: []string{"employment"},
		Hours: &candidate.Hours{Mode: "full_time"}, TimeZones: []string{"europe_africa"}, PreferredLocations: []candidate.Location{{Country: "PT"}}},
		candidate.WorkAuthorization{Country: "EU", Status: "eligible"})
	f := score(p, vacancy{Location: "Remote (EU only) · Remote", Text: "Salary: EUR 120,000 per year\nEmployment type: Permanent, full-time"})
	want := map[string]fit.Tile{
		"Money":     {Value: "€120k a year", Converted: "≈ €120,000", Line: "Pays €120k a year — meets your minimum", Verdict: "✓"},
		"Location":  {Value: "Remote (EU only)", Line: "Remote (EU only) — you prefer PT · Remote work is open to you there · Time zone fits Europe & Africa", Verdict: "✓"},
		"Work mode": {Value: "remote", Line: "Remote only — you want onsite", Verdict: "✕"},
		"Contract":  {Value: "employment · full-time", Line: "Employment — you want employment · Full-time — you want full-time", Verdict: "✓"},
	}
	for _, tile := range f.Tiles {
		w := want[tile.Name]
		if tile.Value != w.Value || tile.Converted != w.Converted || tile.Line != w.Line || tile.Verdict != w.Verdict {
			t.Errorf("%s tile = %+v, want %+v", tile.Name, tile, w)
		}
	}
	for i, names := range [][]string{{"money"}, {"location", "remote_restriction", "time_zone"}, {"work_mode"}, {"engagement", "hours"}} {
		mismatch, match := false, false
		for _, n := range names {
			u := criterion(t, f, n)
			mismatch, match = mismatch || (u >= 0 && u < 1), match || u == 1
		}
		if got := f.Tiles[i].Verdict; (got == "✕") != mismatch || (got == "✓") != (match && !mismatch) {
			t.Errorf("%s tile %s disagrees with %v", f.Tiles[i].Name, got, names)
		}
	}
	noEng := score(goals(candidate.GoalsView{Engagement: []string{"employment"}}), vacancy{Title: "Builder"})
	for _, tile := range noEng.Tiles {
		if tile.Name == "Contract" && (tile.Value != "Engagement not stated" || tile.Verdict != "?") {
			t.Errorf("no engagement stated: Contract tile = %+v", tile)
		}
	}
}

func TestRequirementLineIsDecidedByConfirmedFactThenYearsThenSkills(t *testing.T) {
	p := fit.ProfileView{
		Goals:        candidate.GoalsView{TargetRoles: []string{"Engineer"}},
		Skills:       []candidate.Skill{{Name: "Go"}},
		Confirmed:    []candidate.ConfirmedFact{{Text: "Security clearance", Have: true}, {Text: "Driving licence", Have: false}},
		DerivedYears: candidate.DerivedYears{Total: 8, Engineering: 5},
	}
	cases := []struct {
		line, verdict, evidence string
	}{
		{"Security clearance", "✓", "Confirmed by you"},
		{"Driving licence", "✕", "Confirmed by you"},
		{"5+ years of software engineering", "?", "5 years"},
		{"Golang expertise", "✓", "Go"},
	}
	var reqs []fit.Requirement
	for _, c := range cases {
		reqs = append(reqs, fit.Requirement{Text: c.line, Level: "required"})
	}
	f := fit.Score(p, fit.Vacancy{Title: "Engineer", Full: true, Analysed: true, Requirements: reqs}, rules())
	if len(f.Requirements) != len(cases) {
		t.Fatalf("lines = %d, want %d", len(f.Requirements), len(cases))
	}
	for i, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := f.Requirements[i]; got.Verdict != c.verdict || got.Evidence != c.evidence {
				t.Errorf("verdict %q evidence %q, want %q %q", got.Verdict, got.Evidence, c.verdict, c.evidence)
			}
		})
	}
}
