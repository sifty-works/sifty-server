package source

import (
	"context"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var srcLever Module = &leverSource{board{hosts: []string{"jobs.lever.co", "jobs.eu.lever.co"}, depth: 2}}

type leverSource struct{ board }

func (s *leverSource) Name() string          { return "lever" }
func (s *leverSource) Match(u *url.URL) bool { return s.owns(u) && isRoot(u) }

func (s *leverSource) Group(u *url.URL) string {
	return "api." + strings.ToLower(u.Host)[5:]
}

func (s *leverSource) List(ctx context.Context, f fetch.Fetcher, cfg Site) ([]listing.Posting, error) {
	boardURL, err := url.Parse(cfg.Address)
	if err != nil {
		return nil, ErrNothingRecognised
	}
	site := boardSlug(boardURL)
	apiURL := "https://" + s.Group(boardURL) + "/v0/postings/" + site + "?mode=json"
	var body []lvPosting
	if err := fetchJSON(ctx, f, apiURL, &body); err != nil {
		return nil, err
	}
	company := trim(cfg.Name)
	guessed := company == ""
	if company == "" {
		company = site
	}
	var postings []listing.Posting
	for _, post := range body {
		var tb strings.Builder
		tb.WriteString(post.Description)
		for _, lst := range post.Lists {
			if t := trim(lst.Text); t != "" {
				tb.WriteString("<h3>" + html.EscapeString(t) + "</h3>")
			}
			if c := trim(lst.Content); c != "" {
				tb.WriteString("<ul>" + c + "</ul>")
			}
		}
		tb.WriteString(post.Additional)
		var loc string
		if len(post.Categories.AllLocations) > 0 {
			loc = strings.Join(post.Categories.AllLocations, " · ")
		} else {
			loc = post.Categories.Location
		}
		var salary string
		if sr := post.SalaryRange; sr != nil && (sr.Min != 0 || sr.Max != 0) {
			salary = salaryRangeText(sr.Currency, float64(sr.Min), float64(sr.Max), sr.Interval)
		}
		p := listing.Posting{
			URL:            post.HostedURL,
			ApplyURL:       post.HostedURL,
			Title:          post.Text,
			Company:        company,
			CompanyGuessed: guessed,
			Location:       loc,
			Tags:           []string{post.Categories.Team, post.Categories.Department},
			Fields:         setFields("employment_type", post.Categories.Commitment, "workplace_type", post.WorkplaceType, "salary", salary),
			Text:           tb.String(),
		}
		if post.CreatedAt > 0 {
			p.Published = time.UnixMilli(post.CreatedAt).UTC()
			p.PublishedBasis = "createdAt"
		}
		if p, ok := finalise(p, ""); ok {
			postings = append(postings, p)
		}
	}
	return postings, nil
}

type lvPosting struct {
	Text          string       `json:"text"`
	HostedURL     string       `json:"hostedUrl"`
	CreatedAt     int64        `json:"createdAt"`
	Categories    lvCategories `json:"categories"`
	WorkplaceType string       `json:"workplaceType"`
	SalaryRange   *lvSalary    `json:"salaryRange"`
	Description   string       `json:"description"`
	Lists         []lvList     `json:"lists"`
	Additional    string       `json:"additional"`
}

type lvCategories struct {
	Location     string   `json:"location"`
	AllLocations []string `json:"allLocations"`
	Team         string   `json:"team"`
	Department   string   `json:"department"`
	Commitment   string   `json:"commitment"`
}

type lvSalary struct {
	Min      int    `json:"min"`
	Max      int    `json:"max"`
	Currency string `json:"currency"`
	Interval string `json:"interval"`
}

type lvList struct {
	Text    string `json:"text"`
	Content string `json:"content"`
}
