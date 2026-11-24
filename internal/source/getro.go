package source

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/listing"
)

type ndLocationDetail struct {
	Name string `json:"name"`
}

type ndJob struct {
	Title                string             `json:"title"`
	Slug                 string             `json:"slug"`
	ApplyURL             string             `json:"url"`
	Locations            []string           `json:"locations"`
	LocationDetails      []ndLocationDetail `json:"locationDetails"`
	CreatedAt            float64            `json:"createdAt"`
	WorkMode             string             `json:"workMode"`
	CompensationMinCents int                `json:"compensationAmountMinCents"`
	CompensationMaxCents int                `json:"compensationAmountMaxCents"`
	CompensationCurrency string             `json:"compensationCurrency"`
	CompensationPeriod   string             `json:"compensationPeriod"`
	Org                  struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"organization"`
}

func parseGetro(body []byte, base string, _ Site) ([]listing.Posting, bool) {
	raw := findScript(body, `id="__NEXT_DATA__"`)
	var nd struct {
		Props struct {
			PageProps struct {
				InitialState struct {
					Jobs struct {
						Found []ndJob `json:"found"`
					} `json:"jobs"`
				} `json:"initialState"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &nd) != nil {
		return nil, false
	}
	var out []listing.Posting
	for _, f := range nd.Props.PageProps.InitialState.Jobs.Found {
		if f.Title == "" || f.Org.Name == "" || f.Slug == "" {
			continue
		}
		var locParts []string
		for _, ld := range f.LocationDetails {
			if n := trim(ld.Name); n != "" {
				locParts = append(locParts, n)
			}
		}
		if len(locParts) == 0 {
			locParts = f.Locations
		}
		salary := salaryRangeText(f.CompensationCurrency, float64(f.CompensationMinCents)/100, float64(f.CompensationMaxCents)/100, f.CompensationPeriod)
		p := listing.Posting{
			URL:      resolveURL("/companies/"+f.Org.Slug+"/jobs/"+f.Slug, base),
			Title:    f.Title,
			Company:  f.Org.Name,
			Location: strings.Join(locParts, " · "),
			Fields:   setFields("workplace_type", f.WorkMode, "salary", salary),
		}
		p.ApplyURL = resolveURL(f.ApplyURL, base)
		if f.CreatedAt > 0 {
			sec := int64(f.CreatedAt)
			if sec > 1e10 {
				sec /= 1000
			}
			p.Published, p.PublishedBasis = time.Unix(sec, 0).UTC(), "created_at"
		}
		if p, ok := finalise(p, base); ok {
			out = append(out, p)
		}
	}
	return out, len(out) > 0
}
