package fit_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/fit"
)

func num(x float64) *float64 { return &x }

func preferredPlaces(values ...candidate.Location) candidate.PlaceBuckets {
	return candidate.PlaceBuckets{Preferred: values}
}

func preferred(values ...string) candidate.Buckets { return candidate.Buckets{Preferred: values} }

func rules() fit.Rules {
	d := config.Defaults()
	return fit.Rules{Weights: d.Weights, Rates: d.Rates}
}

type vacancy struct {
	Title, Company, Location, Domain string
	Tags                             []string
	Fields                           map[string]string
	Text                             string
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
	if v.Domain != "" {
		set.Facts = append(set.Facts, facts.Fact{Kind: facts.Domain, Value: v.Domain, Source: facts.Source{Way: facts.Model, Origin: facts.Employer}})
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
		{"role without targets", "role", goals(candidate.GoalsView{WorkMode: preferred("remote")}), vacancy{Title: "Go Engineer"}, -1},
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
		{"preferred country", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "DE"})}), vacancy{Location: "Berlin, Germany"}, 1},
		{"a city of another name is in no list", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "DE", City: "Munich"})}), vacancy{Location: "Berlin, Germany"}, -1},
		{"a country without a city meets a preferred city", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "DE", City: "Munich"})}), vacancy{Location: "Germany"}, 1},
		{"a country without a city does not meet an unwanted city", "location", goals(candidate.GoalsView{Locations: candidate.PlaceBuckets{Unwanted: []candidate.Location{{Country: "DE", City: "Munich"}}}}), vacancy{Location: "Germany"}, -1},
		{"possible place", "location", goals(candidate.GoalsView{Locations: candidate.PlaceBuckets{Possible: []candidate.Location{{Country: "DE"}}}}), vacancy{Location: "Berlin, Germany"}, 0.75},
		{"unwanted place", "location", goals(candidate.GoalsView{Locations: candidate.PlaceBuckets{Unwanted: []candidate.Location{{Country: "US"}}}}), vacancy{Location: "Austin, Texas"}, 0.25},
		{"never place", "location", goals(candidate.GoalsView{Locations: candidate.PlaceBuckets{Never: []candidate.Location{{Country: "US"}}}}), vacancy{Location: "Austin, Texas"}, 0},
		{"the stricter entry holds for one place", "location", goals(candidate.GoalsView{Locations: candidate.PlaceBuckets{Preferred: []candidate.Location{{Country: "EU"}}, Never: []candidate.Location{{Country: "DE"}}}}), vacancy{Location: "Berlin, Germany"}, 0},
		{"region covers country", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "EU"})}), vacancy{Location: "Lisbon, Portugal"}, 1},
		{"a place in no list is left out", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "FR"})}), vacancy{Location: "Austin, Texas"}, -1},
		{"city only", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "FR"})}), vacancy{Location: "Springfield"}, -1},
		{"no preferred locations", "location", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), vacancy{Location: "Remote"}, -1},
		{"remote without a place cannot be compared", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "FR"})}), vacancy{Location: "Remote"}, -1},
		{"remote with only a time zone cannot be compared", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "FR"})}), vacancy{Location: "Remote", Text: "Time zone: CET (+/- 3 hours)"}, -1},
		{"unrestricted remote", "location", goals(candidate.GoalsView{Locations: preferredPlaces(candidate.Location{Country: "FR"})}), vacancy{Location: "Remote · Anywhere"}, 1},
		{"telecommute is unrestricted remote", "remote_restriction", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), vacancy{Location: "TELECOMMUTE · Anywhere"}, 1},
		{"work mode shared", "work_mode", goals(candidate.GoalsView{WorkMode: preferred("remote", "hybrid")}), vacancy{Text: "Work mode: Hybrid"}, 1},
		{"work mode in no list is left out", "work_mode", goals(candidate.GoalsView{WorkMode: preferred("remote")}), vacancy{Location: "Onsite"}, -1},
		{"possible work mode", "work_mode", goals(candidate.GoalsView{WorkMode: candidate.Buckets{Preferred: []string{"remote"}, Possible: []string{"hybrid"}}}), vacancy{Text: "Work mode: Hybrid"}, 0.75},
		{"unwanted work mode", "work_mode", goals(candidate.GoalsView{WorkMode: candidate.Buckets{Unwanted: []string{"hybrid"}}}), vacancy{Text: "Work mode: Hybrid"}, 0.25},
		{"never work mode", "work_mode", goals(candidate.GoalsView{WorkMode: candidate.Buckets{Preferred: []string{"remote"}, Never: []string{"onsite"}}}), vacancy{Location: "Onsite"}, 0},
		{"the best of several work modes counts", "work_mode", goals(candidate.GoalsView{WorkMode: candidate.Buckets{Preferred: []string{"hybrid"}, Never: []string{"onsite"}}}), vacancy{Text: "Work mode: Hybrid or Onsite"}, 1},
		{"work mode not stated", "work_mode", goals(candidate.GoalsView{WorkMode: preferred("remote")}), vacancy{}, -1},
		{"restricted remote is compared by the work mode lists", "work_mode", goals(candidate.GoalsView{WorkMode: preferred("remote")}), vacancy{Location: "Remote (US only) · Remote"}, 1},
		{"preferred domain", "domain", goals(candidate.GoalsView{Domain: preferred("exchange")}), vacancy{Domain: "exchange"}, 1},
		{"never domain", "domain", goals(candidate.GoalsView{Domain: candidate.Buckets{Preferred: []string{"exchange"}, Never: []string{"gambling"}}}), vacancy{Domain: "gambling"}, 0},
		{"domain not read cannot be compared", "domain", goals(candidate.GoalsView{Domain: preferred("exchange")}), vacancy{}, -1},
		{"engagement from tag", "engagement", goals(candidate.GoalsView{EngagementType: preferred("contract")}), vacancy{Tags: []string{"Contract"}}, 1},
		{"unwanted engagement", "engagement", goals(candidate.GoalsView{EngagementType: candidate.Buckets{Preferred: []string{"contract"}, Unwanted: []string{"employment"}}}), vacancy{Fields: map[string]string{"employment_type": "PERMANENT"}}, 0.25},
		{"engagement in no list is left out", "engagement", goals(candidate.GoalsView{EngagementType: preferred("contract")}), vacancy{Fields: map[string]string{"employment_type": "PERMANENT"}}, -1},
		{"internship in the unwanted list", "engagement", goals(candidate.GoalsView{EngagementType: candidate.Buckets{Preferred: []string{"contract"}, Unwanted: []string{"internship"}}}), vacancy{Fields: map[string]string{"employment_type": "INTERN"}}, 0.25},
		{"internship in the never list", "engagement", goals(candidate.GoalsView{EngagementType: candidate.Buckets{Preferred: []string{"employment"}, Never: []string{"internship"}}}), vacancy{Fields: map[string]string{"employment_type": "INTERN"}}, 0},
		{"no engagement stated cannot be compared with an employment goal", "engagement", goals(candidate.GoalsView{EngagementType: preferred("employment")}), vacancy{Title: "Builder"}, -1},
		{"no engagement stated cannot be compared with a contract goal", "engagement", goals(candidate.GoalsView{EngagementType: preferred("contract")}), vacancy{Title: "Builder"}, -1},
		{"no engagement goal leaves criterion not comparable", "engagement", goals(candidate.GoalsView{WorkMode: preferred("remote")}), vacancy{Title: "Builder"}, -1},
		{"hours mode", "hours", goals(candidate.GoalsView{WorkingTime: preferred("part_time")}), vacancy{Text: "Hours: part-time"}, 1},
		{"unwanted working time", "hours", goals(candidate.GoalsView{WorkingTime: candidate.Buckets{Preferred: []string{"part_time"}, Unwanted: []string{"full_time"}}}), vacancy{Tags: []string{"Full-time"}}, 0.25},
		{"weekly range meets", "hours", goals(candidate.GoalsView{WorkingTime: preferred("part_time"), Hours: &candidate.Hours{Weekly: num(25)}}), vacancy{Text: "Hours: part-time, 20-30 hours per week"}, 1},
		{"weekly range above", "hours", goals(candidate.GoalsView{WorkingTime: preferred("part_time"), Hours: &candidate.Hours{WeeklyMax: num(15)}}), vacancy{Text: "Hours: part-time, 20 to 30 h/week"}, 0},
		{"weekly hours of a full-time goal not compared", "hours", goals(candidate.GoalsView{WorkingTime: preferred("full_time"), Hours: &candidate.Hours{Weekly: num(10)}}), vacancy{Text: "Hours: full-time, 40 hours per week"}, 1},
		{"weekly hours not stated", "hours", goals(candidate.GoalsView{WorkingTime: preferred("part_time"), Hours: &candidate.Hours{Weekly: num(25)}}), vacancy{Text: "Hours: part-time"}, 1},
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
		{"no authorization for the country while the profile names others", "Remote (US only)", []candidate.WorkAuthorization{eligibleEU}, 0},
		{"an eligible authorization in one country of the free movement zone covers the others", "Remote - Spain", []candidate.WorkAuthorization{{Country: "DE", Status: "eligible"}}, 1},
		{"restricted remote and no authorization in the profile", "Remote (US only)", nil, -1},
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
	t.Run("an unknown criterion counts as half-way at half its weight", func(t *testing.T) {
		f := fit.Score(p, fromCode(v), r)
		if f.State != fit.Scored || f.Score != 100 || f.ScoredOn != 2 || f.Of != 2 {
			t.Errorf("got %s %d on %d of %d, want scored 100 on 2 of 2", f.State, f.Score, f.ScoredOn, f.Of)
		}
		pw := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, WorkMode: preferred("remote"), Compensation: eur})
		if f = fit.Score(pw, fromCode(v), r); f.Score != 98 || f.ScoredOn != 2 || f.Of != 3 {
			t.Errorf("an unknown work mode: got %d on %d of %d, want 98 on 2 of 3", f.Score, f.ScoredOn, f.Of)
		}
		hybrid := fromCode(vacancy{Title: "Go Engineer", Text: "Salary: EUR 100,000 per year\nThis is a hybrid role."})
		if f = fit.Score(pw, hybrid, r); f.Score != 100 || f.ScoredOn != 2 || f.Of != 2 {
			t.Errorf("a work mode in none of the lists: got %d on %d of %d, want 100 on 2 of 2", f.Score, f.ScoredOn, f.Of)
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
		pw := goals(candidate.GoalsView{WorkMode: preferred("remote"), WorkingTime: preferred("part_time")})
		f := fit.Score(pw, fit.Vacancy{Title: "Builder", Full: true, Analysed: true}, rules())
		if f.State != fit.NoFacts || f.Score != 0 || f.ScoredOn != 0 || f.Bucket != "Not scored" || f.Broken {
			t.Errorf("got %+v, want no number scored on 0", f)
		}
	})
}

func TestBrokenMustCapsByItsSourceAndUnknownMustCostsHalfItsWeight(t *testing.T) {
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
	remote := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur, WorkMode: candidate.Buckets{Preferred: []string{"remote"}, Never: []string{"onsite"}}})
	cases := []struct {
		name     string
		p        fit.ProfileView
		facts    []facts.Fact
		score    int
		broken   bool
		reason   string
		scoredOn int
	}{
		{"work mode broken by the text", remote, []facts.Fact{money(120000, inText), fact(facts.WorkMode, "onsite", inText)}, 15, true, "Onsite — in your Never list", 3},
		{"an unwanted engagement from the text caps nothing", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, EngagementType: candidate.Buckets{Preferred: []string{"contract"}, Unwanted: []string{"employment"}}}),
			[]facts.Fact{fact(facts.Engagement, "employment", inText)}, 92, false, "Employment — unwanted", 2},
		{"an unwanted work mode from the text caps nothing", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, WorkMode: candidate.Buckets{Preferred: []string{"remote"}, Unwanted: []string{"onsite"}}}),
			[]facts.Fact{fact(facts.WorkMode, "onsite", inText)}, 90, false, "Onsite — unwanted", 2},
		{"required language missing from My data", fit.ProfileView{Goals: candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}, Languages: []candidate.Language{{Name: "English"}}},
			[]facts.Fact{fact(facts.Language, "Mandarin", inText)}, 15, true, "Requires Mandarin — not in your languages", 1},
		{"work authorization broken by the text", fit.ProfileView{Goals: candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}, Authorizations: []candidate.WorkAuthorization{{Country: "US", Status: "need_visa"}}},
			[]facts.Fact{fact(facts.WorkMode, "remote", inText), fact(facts.Place, "US", inText)}, 15, true, "Remote only in", 2},
		{"work mode broken only by an aggregator field", remote, []facts.Fact{money(120000, inText), fact(facts.WorkMode, "onsite", field)}, 40, false, "Onsite — in your Never list", 3},
		{"unknown must counts half-way and says why", remote, []facts.Fact{money(120000, inText)}, 98, false, "Work mode not stated", 2},
		{"required language and no language in My data", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}),
			[]facts.Fact{fact(facts.Language, "Mandarin", inText)}, 100, false, "Requires Mandarin — your languages are not listed", 1},
		{"gambling in the Never list read by the model caps at 40 and is not broken", goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Domain: candidate.Buckets{Preferred: []string{"exchange"}, Never: []string{"gambling"}}}),
			[]facts.Fact{{Kind: facts.Domain, Value: "gambling", Source: facts.Source{Way: facts.Model, Origin: facts.Employer}}}, 40, false, "Gambling — in your Never list", 2},
		{"a domain list alone is a goal", goals(candidate.GoalsView{Domain: candidate.Buckets{Unwanted: []string{"exchange"}}}),
			[]facts.Fact{{Kind: facts.Domain, Value: "exchange", Source: facts.Source{Way: facts.Model, Origin: facts.Employer}}}, 25, false, "Exchange — unwanted", 1},
		{"remote only in a country the profile does not list", fit.ProfileView{Goals: candidate.GoalsView{TargetRoles: []string{"Go Engineer"}}, Authorizations: []candidate.WorkAuthorization{{Country: "DE", Status: "eligible"}}},
			[]facts.Fact{fact(facts.WorkMode, "remote", inText), fact(facts.Place, "US", inText)}, 15, true, "Remote only in United States — not in your work authorizations", 2},
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
	p := goals(candidate.GoalsView{Compensation: eur, WorkMode: candidate.Buckets{Preferred: []string{"onsite"}, Never: []string{"remote"}}, EngagementType: preferred("employment"),
		WorkingTime: candidate.Buckets{Possible: []string{"full_time"}}, TimeZones: []string{"europe_africa"}, Locations: preferredPlaces(candidate.Location{Country: "PT"}), Domain: preferred("exchange")},
		candidate.WorkAuthorization{Country: "EU", Status: "eligible"})
	p.Languages = []candidate.Language{{Name: "English"}}
	f := score(p, vacancy{Domain: "exchange", Location: "Remote (EU only) · Remote", Text: "Salary: EUR 120,000 per year\nEmployment type: Permanent, full-time"})
	want := []fit.Tile{
		{Name: "Money", Value: "€120k a year", Converted: "≈ €120,000", Line: "Pays €120k a year — meets your minimum", Verdict: "✓"},
		{Name: "Location", Value: "Remote (EU only)", Line: "Remote (EU only) — preferred", Verdict: "✓", Bucket: "preferred"},
		{Name: "Work mode", Value: "remote", Line: "Remote — in your Never list", Verdict: "✕", Bucket: "never"},
		{Name: "Contract", Value: "employment", Line: "Employment — preferred", Verdict: "✓", Bucket: "preferred"},
		{Name: "Time zone", Value: "Europe & Africa", Line: "Time zone fits Europe & Africa", Verdict: "✓"},
		{Name: "Languages", Value: "Not stated", Line: "Not stated", Verdict: "?"},
		{Name: "Hours", Value: "full-time", Line: "Full-time — possible", Verdict: "✓", Bucket: "possible"},
		{Name: "Work authorization", Value: "Remote (EU only)", Line: "Remote work is open to you there", Verdict: "✓"},
		{Name: "Domain", Value: "Exchange", Line: "Exchange — preferred · read by the model", Verdict: "✓", Bucket: "preferred"},
	}
	if len(f.Tiles) != len(want) {
		t.Fatalf("got %d tiles, want %d: %+v", len(f.Tiles), len(want), f.Tiles)
	}
	for i, w := range want {
		if tile := f.Tiles[i]; tile != w {
			t.Errorf("tile %d = %+v, want %+v", i, tile, w)
		}
	}
	for i, name := range []string{"money", "location", "work_mode", "engagement", "time_zone", "languages", "hours", "remote_restriction", "domain"} {
		var c fit.Criterion
		for _, cr := range f.Criteria {
			if cr.Name == name {
				c = cr
			}
		}
		if got := f.Tiles[i].Verdict; (got == "✕") != (c.Outcome == fit.Mismatch) || (got == "✓") != (c.Outcome == fit.Match) {
			t.Errorf("%s tile %s disagrees with its criterion %s", f.Tiles[i].Name, got, name)
		}
	}
	tile := func(f fit.Fit, name string) fit.Tile {
		for _, tl := range f.Tiles {
			if tl.Name == name {
				return tl
			}
		}
		t.Fatalf("no %s tile in %+v", name, f.Tiles)
		return fit.Tile{}
	}
	lang := tile(score(p, vacancy{Text: "Requirements\n- Fluent Mandarin required"}), "Languages")
	if lang.Value != "Mandarin" || lang.Verdict != "✕" || lang.Line != "Requires Mandarin — not in your languages" {
		t.Errorf("a language the user lacks: Languages tile = %+v", lang)
	}
	noZone := tile(score(goals(candidate.GoalsView{EngagementType: preferred("employment")}), vacancy{Title: "Builder"}), "Time zone")
	if noZone.Value != "Not stated" || noZone.Verdict != "?" || noZone.Line != "Not stated" {
		t.Errorf("no time zone stated, no time zone goal: Time zone tile = %+v", noZone)
	}
	if c := tile(score(goals(candidate.GoalsView{EngagementType: preferred("employment")}), vacancy{Title: "Builder"}), "Contract"); c.Value != "Not stated" || c.Verdict != "?" {
		t.Errorf("no engagement stated: Contract tile = %+v", c)
	}
}

func TestKeyFactsDoNotChangeFitsYou(t *testing.T) {
	p := goals(candidate.GoalsView{TargetRoles: []string{"Engineer"}})
	p.DerivedYears = candidate.DerivedYears{Total: 1}
	plain := fit.Vacancy{Title: "Engineer", Full: true, Analysed: true}
	stated := plain
	stated.Years, stated.Seniority, stated.Stage = 9, "Senior", "Series B"
	a, b := fit.Score(p, plain, rules()), fit.Score(p, stated, rules())
	if a.State != fit.Scored || a.Score != b.Score || a.ScoredOn != b.ScoredOn || a.Of != b.Of || len(a.Criteria) != len(b.Criteria) {
		t.Errorf("Fit with key facts = %d on %d of %d, without = %d on %d of %d", b.Score, b.ScoredOn, b.Of, a.Score, a.ScoredOn, a.Of)
	}
}

func TestApplyByTileIsClosedOnlyAfterItsDay(t *testing.T) {
	p := goals(candidate.GoalsView{TargetRoles: []string{"Engineer"}})
	p.Today = time.Date(2026, 12, 10, 15, 30, 0, 0, time.UTC)
	cases := []struct{ name, deadline, verdict, line string }{
		{"tomorrow is still open", "2026-12-11", "–", ""},
		{"today is the last day", "2026-12-10", "–", "Closes today"},
		{"yesterday is closed", "2026-12-09", "✕", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fit.Score(p, fit.Vacancy{Title: "Engineer", Full: true, Analysed: true, Deadline: tc.deadline}, rules())
			for _, tl := range f.Tiles {
				if tl.Name == "Apply by" {
					if tl.Verdict != tc.verdict || tc.line != "" && tl.Line != tc.line {
						t.Errorf("Apply by tile for %s = %q %q, want %q %q", tc.deadline, tl.Verdict, tl.Line, tc.verdict, tc.line)
					}
					return
				}
			}
			t.Errorf("no Apply by tile in %+v", f.Tiles)
		})
	}
}

func TestConditionTilesSayWhatIsRefusedAndDoNotChangeFitsYou(t *testing.T) {
	p := goals(candidate.GoalsView{TargetRoles: []string{"Engineer"}})
	plain := fit.Vacancy{Title: "Engineer", Full: true, Analysed: true}
	stated := plain
	stated.Deadline, stated.Length, stated.Clearance, stated.Relocation, stated.Visa = "2099-12-01", "6 months", "US citizen", "none", "refused"
	a, b := fit.Score(p, plain, rules()), fit.Score(p, stated, rules())
	if a.State != fit.Scored || a.Score != b.Score || a.ScoredOn != b.ScoredOn || a.Of != b.Of {
		t.Errorf("Fit with condition facts = %d on %d of %d, without = %d on %d of %d", b.Score, b.ScoredOn, b.Of, a.Score, a.ScoredOn, a.Of)
	}
	got := map[string]string{}
	for _, tl := range b.Tiles {
		got[tl.Name] = tl.Value
	}
	if got["Relocation"] != "Not offered" || got["Visa"] != "No sponsorship" {
		t.Errorf("Relocation none and Visa refused show %q and %q", got["Relocation"], got["Visa"])
	}
}

func TestRequirementRowIsDecidedByConfirmedFactThenYears(t *testing.T) {
	p := fit.ProfileView{
		Goals:        candidate.GoalsView{TargetRoles: []string{"Engineer"}},
		Confirmed:    []candidate.ConfirmedFact{{Text: "Security clearance", Have: true}, {Text: "Driving licence", Have: false}},
		DerivedYears: candidate.DerivedYears{Total: 8, Engineering: 5},
	}
	cases := []struct {
		line, verdict, evidence string
	}{
		{"Security clearance", "✓", "Confirmed by you"},
		{"Driving licence", "✕", "Confirmed by you"},
		{"5+ years of software engineering", "?", "5 years"},
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

func TestRequirementsCriterionAveragesSkillsAndOtherRequirements(t *testing.T) {
	p := fit.ProfileView{
		Goals:        candidate.GoalsView{TargetRoles: []string{"Engineer"}},
		Skills:       []candidate.Skill{{Name: "Go", Level: "Beginner"}, {Name: "Rust", Level: "Proficient"}},
		AbsentSkills: []candidate.AbsentSkill{{Name: "Kafka"}},
		Confirmed:    []candidate.ConfirmedFact{{Text: "Driving licence", Have: false}},
	}
	reqs := []fit.Requirement{
		{Text: "Go", Level: "required"},
		{Text: "5+ years of Rust", Level: "required"},
		{Text: "Kafka is a plus", Level: "preferred"},
		{Text: "Driving licence", Level: "required"},
		{Text: "Redis is a plus", Level: "preferred"},
	}
	f := fit.Score(p, fit.Vacancy{Title: "Engineer", Full: true, Analysed: true, Requirements: reqs}, rules())
	if got, want := criterion(t, f, "requirements"), 3.0/7; math.Abs(got-want) > 1e-9 {
		t.Errorf("requirements utility = %v, want %v", got, want)
	}
}
