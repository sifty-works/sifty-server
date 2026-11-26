package inbox_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
)

func saveOffer(t *testing.T, d *db.DB, id int64, money string, perks ...string) {
	t.Helper()
	found := []facts.Fact{{Kind: facts.Analysed, Value: "test", Source: facts.Source{Way: facts.Vocabulary}}}
	if money != "" {
		found = append(found, facts.Fact{Kind: facts.Money, Value: money, Source: facts.Source{Way: facts.SiteField}})
	}
	for _, p := range perks {
		found = append(found, facts.Fact{Kind: facts.Offer, Value: p, Source: facts.Source{Way: facts.Vocabulary}})
	}
	if err := facts.Save(context.Background(), d, id, 1, "offer", found); err != nil {
		t.Fatalf("save facts: %v", err)
	}
}

func offerOf(body map[string]any) map[string]any {
	o, _ := body["offer"].(map[string]any)
	return o
}

func offerStrings(offer map[string]any, key string) []string {
	var out []string
	list, _ := offer[key].([]any)
	for _, v := range list {
		if tile, ok := v.(map[string]any); ok {
			out = append(out, fmt.Sprintf("%s=%s=%s", tile["key"], tile["label"], tile["value"]))
			continue
		}
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func bandTileValue(body map[string]any, name string) string {
	band, _ := body["band"].(map[string]any)
	for _, t := range band["tiles"].([]any) {
		tile, _ := t.(map[string]any)
		if tile["name"] == name {
			v, _ := tile["value"].(string)
			return v
		}
	}
	return ""
}

func TestVacancyOffer_SalaryTileAndItems(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	id := add(t, newCat(d), postWithText("https://example.com/jobs/offer-1", "Backend Engineer", "Acme", "job text"))
	saveOffer(t, d, id, `{"min":90000,"max":90000,"currency":"EUR","period":"year"}`,
		`{"key":"section"}`,
		`{"key":"equity","label":"Equity","value":"Amount not stated"}`,
		`{"key":"timeOff","label":"Time off","value":"30 days of paid time off"}`,
		`{"value":"Free lunch on Fridays"}`)
	body, _ := apiGetRaw(t, newServer(t, d, serverOpts{rules: defaultRules}),
		fmt.Sprintf("/api/vacancies/%d?profile=1", id))

	money := bandTileValue(body, "Money")
	if money == "" || money == "Not stated" {
		t.Fatalf("the band states no money: %q", money)
	}
	want := "salary=Base salary=" + money +
		"; equity=Equity=Amount not stated; timeOff=Time off=30 days of paid time off"
	if got := strings.Join(offerStrings(offerOf(body), "tiles"), "; "); got != want {
		t.Errorf("tiles: got %q, want %q", got, want)
	}
	if got := strings.Join(offerStrings(offerOf(body), "items"), "; "); got != "Free lunch on Fridays" {
		t.Errorf("items: got %q, want %q", got, "Free lunch on Fridays")
	}
}

func TestVacancyOffer_EmptyStates(t *testing.T) {
	for i, tc := range []struct {
		name, want string
		perks      []string
	}{
		{"a vacancy whose text was never fetched",
			"No Compensation or Benefits section recognised in this listing.", nil},
		{"a section holding no item",
			"Section found but no specific items matched.", []string{`{"key":"section"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, nil, []string{"description"}, nil)
			id := add(t, newCat(d), postWithText(
				fmt.Sprintf("https://example.com/jobs/offer-e%d", i), "Engineer", "Acme", "job text"))
			saveOffer(t, d, id, "", tc.perks...)
			body, _ := apiGetRaw(t, newServer(t, d, serverOpts{rules: defaultRules}),
				fmt.Sprintf("/api/vacancies/%d?profile=1", id))
			offer := offerOf(body)
			if e, _ := offer["empty"].(string); e != tc.want {
				t.Errorf("empty: got %q, want %q", e, tc.want)
			}
			if tiles, items := offerStrings(offer, "tiles"), offerStrings(offer, "items"); len(tiles)+len(items) > 0 {
				t.Errorf("got tiles %v and items %v, want none", tiles, items)
			}
		})
	}
}
