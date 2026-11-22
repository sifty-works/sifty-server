package fit_test

import (
	"math"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/reading"
)

func num(x float64) *float64 { return &x }

func rules() fit.Rules {
	d := config.Defaults()
	return fit.Rules{Weights: d.Weights, Rates: d.Rates}
}

func score(p fit.ProfileView, v reading.Vacancy) fit.Fit {
	v.Full = true
	return fit.Score(p, reading.FromCode(v), rules())
}

func criterion(t *testing.T, f fit.Fit, name string) float64 {
	t.Helper()
	for _, c := range f.Criteria {
		if c.Name == name {
			if !c.Known {
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
		v               reading.Vacancy
		want            float64
	}{
		{"role overlap", "role", goals(candidate.GoalsView{TargetRoles: []string{"Backend Engineer"}}), reading.Vacancy{Title: "Senior Backend Engineer"}, 0.8},
		{"role disjoint", "role", goals(candidate.GoalsView{TargetRoles: []string{"Designer"}}), reading.Vacancy{Title: "Go Engineer"}, 0},
		{"role without targets", "role", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), reading.Vacancy{Title: "Go Engineer"}, -1},
		{"money range above minimum", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: €95,000 - €110,000 per year"}, 1},
		{"money top below minimum", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: EUR 60k - 72k per year"}, 0.8},
		{"top at or above the minimum", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: EUR 80k - 100k per year"}, 1},
		{"monthly sum times twelve", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: EUR 7,000 per month"}, 84000.0 / 90000},
		{"hourly sum times 2080", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: EUR 50 per hour"}, 1},
		{"converted by rate", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: USD 90,000 per year"}, 0.92},
		{"no rate for currency", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: CHF 50,000 per year", Fields: nil}, 0.6},
		{"net against gross", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: EUR 100,000 per year net"}, -1},
		{"floor only", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Text: "Salary: from EUR 100,000 per year"}, -1},
		{"source field salary", "money", goals(candidate.GoalsView{Compensation: eur}), reading.Vacancy{Fields: map[string]string{"salary": "95000–120000 EUR per year"}}, 1},
		{"preferred country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "DE"}}}), reading.Vacancy{Location: "Berlin, Germany"}, 1},
		{"preferred city elsewhere", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "DE", City: "Munich"}}}), reading.Vacancy{Location: "Berlin, Germany"}, 0},
		{"region covers country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "EU"}}}), reading.Vacancy{Location: "Lisbon, Portugal"}, 1},
		{"other country", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), reading.Vacancy{Location: "Austin, Texas"}, 0},
		{"city only", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), reading.Vacancy{Location: "Springfield"}, -1},
		{"no preferred locations", "location", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), reading.Vacancy{Location: "Remote"}, -1},
		{"unrestricted remote", "location", goals(candidate.GoalsView{PreferredLocations: []candidate.Location{{Country: "FR"}}}), reading.Vacancy{Location: "Remote · Anywhere"}, 1},
		{"telecommute is unrestricted remote", "remote_restriction", goals(candidate.GoalsView{TargetRoles: []string{"x"}}), reading.Vacancy{Location: "TELECOMMUTE · Anywhere"}, 1},
		{"work mode shared", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote", "hybrid"}}), reading.Vacancy{Text: "Work mode: Hybrid"}, 1},
		{"work mode disjoint", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), reading.Vacancy{Location: "Onsite"}, 0},
		{"work mode not stated", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), reading.Vacancy{}, -1},
		{"restricted remote leaves work mode to the restriction", "work_mode", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), reading.Vacancy{Location: "Remote (US only) · Remote"}, -1},
		{"engagement from tag", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), reading.Vacancy{Tags: []string{"Contract"}}, 1},
		{"engagement disjoint", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), reading.Vacancy{Fields: map[string]string{"employment_type": "FULL_TIME"}}, 0},
		{"no engagement stated matches employment goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"employment"}}), reading.Vacancy{Title: "Builder"}, 1},
		{"no engagement stated mismatches contract goal", "engagement", goals(candidate.GoalsView{Engagement: []string{"contract"}}), reading.Vacancy{Title: "Builder"}, 0},
		{"no engagement goal leaves criterion not comparable", "engagement", goals(candidate.GoalsView{WorkModes: []string{"remote"}}), reading.Vacancy{Title: "Builder"}, -1},
		{"hours mode", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time"}}), reading.Vacancy{Text: "Hours: part-time"}, 1},
		{"hours other mode", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time"}}), reading.Vacancy{Tags: []string{"Full-time"}}, 0},
		{"weekly range meets", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", Weekly: num(25)}}), reading.Vacancy{Text: "Hours: part-time, 20-30 hours per week"}, 1},
		{"weekly range above", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", WeeklyMax: num(15)}}), reading.Vacancy{Text: "Hours: part-time, 20 to 30 h/week"}, 0},
		{"weekly hours of a full-time goal not compared", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "full_time", Weekly: num(10)}}), reading.Vacancy{Text: "Hours: full-time, 40 hours per week"}, 1},
		{"weekly hours not stated", "hours", goals(candidate.GoalsView{Hours: &candidate.Hours{Mode: "part_time", Weekly: num(25)}}), reading.Vacancy{Text: "Hours: part-time"}, -1},
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
		{"unrestricted without user geography", "Remote", nil, 1},
		{"needs a visa for the country", "Remote (US only)", []candidate.WorkAuthorization{eligibleEU, {Country: "US", Status: "need_visa"}}, 0},
		{"no authorization stated for the country", "Remote (US only)", []candidate.WorkAuthorization{eligibleEU}, -1},
		{"unknown place", "Remote - Atlantis", []candidate.WorkAuthorization{eligibleEU}, -1},
		{"not remote", "Lisbon, Portugal", []candidate.WorkAuthorization{eligibleEU}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := reading.Vacancy{Location: c.location}
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
		v    reading.Vacancy
		want float64
	}{
		{"stated offset", reading.Vacancy{Text: "Time zone: UTC+2", Location: "United States"}, 1},
		{"stated label", reading.Vacancy{Text: "Timezone: PST"}, 0},
		{"label widened", reading.Vacancy{Text: "Time zone: EST ± 4 hours"}, 1},
		{"country", reading.Vacancy{Location: "Remote (Germany)"}, 1},
		{"region", reading.Vacancy{Location: "APAC"}, 0},
		{"phrase is not read by code", reading.Vacancy{Text: "Time zone: overlap with Pacific Time"}, -1},
		{"city only", reading.Vacancy{Location: "Springfield"}, -1},
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
		Goals:  candidate.GoalsView{TargetRoles: []string{"Engineer"}},
		Skills: []candidate.Skill{{Name: "Go"}},
	}
	r0 := fit.Rules{Weights: map[string]float64{"role": 1}, Rates: rules().Rates}
	r1 := fit.Rules{Weights: map[string]float64{"role": 1, "requirements": 1}, Rates: rules().Rates}
	v := reading.VacancyReading{
		Title:            "Engineer",
		Full:             true,
		RequirementsRead: true,
		Requirements: []reading.Requirement{
			{Text: "Go", Level: "required"},
			{Text: "Kubernetes", Level: "nice_to_have"},
		},
	}
	noReqV := reading.VacancyReading{Title: "Engineer", Full: true}
	if fit.Score(p, v, r0).Score != fit.Score(p, noReqV, r0).Score {
		t.Error("requirements weight 0: score differs with and without requirements read")
	}
	if fit.Score(p, v, r1).Score == fit.Score(p, noReqV, r1).Score {
		t.Error("requirements weight 1: score identical with and without requirements read")
	}
}

func TestNumberIsWeightedMeanWithUnknownAsHalfAndZeroWeightLeftOut(t *testing.T) {
	p := goals(candidate.GoalsView{TargetRoles: []string{"Go Engineer"}, Compensation: eur})
	v := reading.Vacancy{Title: "Go Engineer", Text: "Salary: EUR 100,000 per year"}
	r := rules()
	f := fit.Score(p, reading.FromCode(reading.Vacancy{Title: v.Title, Text: v.Text, Full: true}), r)
	if want := int(math.Round((0.45 + 0.5*0.35) / 0.8 * 100)); f.State != fit.Scored || f.Score != want || len(f.Criteria) != 8 {
		t.Fatalf("got %s %d with %d criteria, want scored %d", f.State, f.Score, len(f.Criteria), want)
	}
	r.Weights = map[string]float64{"role": 1, "money": 0}
	f = fit.Score(p, reading.FromCode(reading.Vacancy{Title: v.Title, Text: v.Text, Full: true}), r)
	if f.Score != 100 || len(f.Criteria) != 1 || f.Bucket != "Strong" {
		t.Errorf("only role weighted: got %d %s %+v", f.Score, f.Bucket, f.Criteria)
	}
}

func TestNoNumberWithoutFullTextOrGoals(t *testing.T) {
	v := reading.FromCode(reading.Vacancy{Title: "Go Engineer"})
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
		money            reading.Money
		value, converted string
	}{
		{"a range of whole thousands", reading.Money{Min: 190000, Max: 225000, Currency: "USD", Period: "year"}, "$190–225k a year", "≈ €207,000"},
		{"pounds keep their symbol", reading.Money{Min: 40000, Max: 50000, Currency: "GBP", Period: "year"}, "£40–50k a year", "≈ €59,000"},
		{"a currency without a symbol keeps its code", reading.Money{Max: 120000, Currency: "CHF", Period: "year"}, "CHF 120k a year", "≈ €130,000"},
		{"yen keep their code and convert at the rate from the settings", reading.Money{Max: 8000000, Currency: "JPY", Period: "year"}, "JPY 8M a year", "≈ €50,000"},
		{"an amount that is not whole thousands gets one decimal", reading.Money{Max: 7500, Currency: "EUR", Period: "month"}, "€7.5k a month", "≈ €90,000"},
		{"a million and more is written in millions", reading.Money{Max: 1200000, Currency: "USD", Period: "year"}, "$1.2M a year", "≈ €1,104,000"},
		{"ends that differ in suffix carry one each", reading.Money{Min: 900, Max: 1200000, Currency: "EUR", Period: "year"}, "€900–1.2M a year", "≈ €1,200,000"},
		{"an amount below a thousand keeps its digits", reading.Money{Max: 850, Currency: "EUR", Period: "day"}, "€850 a day", "≈ €221,000"},
		{"only a minimum writes one number", reading.Money{Min: 60000, Currency: "EUR", Period: "year"}, "€60k a year", ""},
		{"only a maximum writes one number", reading.Money{Max: 80000, Currency: "EUR", Period: "year"}, "€80k a year", "≈ €80,000"},
		{"no period appends nothing", reading.Money{Max: 95000, Currency: "EUR"}, "€95k", "≈ €95,000"},
		{"an hourly rate reads with the right article", reading.Money{Min: 24, Max: 33, Currency: "USD", Period: "hour"}, "$24–33 an hour", "≈ €63,000"},
		{"a currency without a rate leaves the conversion empty", reading.Money{Min: 500000, Max: 600000, Currency: "SEK", Period: "year"}, "SEK 500–600k a year", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := reading.VacancyReading{Title: "Engineer", Full: true, Money: &c.money}
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
	f := score(p, reading.Vacancy{Location: "Remote (EU only) · Remote", Text: "Salary: EUR 120,000 per year\nEmployment type: Permanent, full-time"})
	want := map[string]fit.Tile{
		"Money":     {Value: "€120k a year", Converted: "≈ €120,000", Line: "Your minimum: EUR 90,000 a year", Verdict: "✓"},
		"Location":  {Value: "Remote (EU only)", Line: "You prefer: PT · Europe & Africa", Verdict: "✓"},
		"Work mode": {Value: "remote", Line: "You want: onsite", Verdict: "✕"},
		"Contract":  {Value: "employment", Line: "You want: employment · full-time", Verdict: "✓"},
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
	noEng := score(goals(candidate.GoalsView{Engagement: []string{"employment"}}), reading.Vacancy{Title: "Builder"})
	for _, tile := range noEng.Tiles {
		if tile.Name == "Contract" && (tile.Value != "Employment (assumed)" || tile.Verdict != "✓") {
			t.Errorf("no engagement stated: Contract tile = %+v", tile)
		}
	}
}
