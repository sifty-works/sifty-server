package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var srcAshby Module = &ashbySource{board{hosts: []string{"jobs.ashbyhq.com"}, depth: 2}}

type ashbySource struct{ board }

func (s *ashbySource) Name() string          { return "ashby" }
func (s *ashbySource) Match(u *url.URL) bool { return s.owns(u) && isRoot(u) }

func (s *ashbySource) Group(u *url.URL) string {
	return "api." + strings.ToLower(u.Host)[5:]
}

func (s *ashbySource) List(ctx context.Context, f fetch.Fetcher, cfg Site) ([]listing.Posting, error) {
	boardURL, err := url.Parse(cfg.Address)
	if err != nil {
		return nil, ErrNothingRecognised
	}
	apiURL := fmt.Sprintf("https://%s/posting-api/job-board/%s?includeCompensation=true", s.Group(boardURL), boardSlug(boardURL))
	var body ashbyResponse
	if err := fetchJSON(ctx, f, apiURL, &body); err != nil {
		return nil, err
	}
	company := trim(cfg.Name)
	guessed := company == ""
	if company == "" {
		company = trim(body.Organization.Name)
	}
	var postings []listing.Posting
	for _, job := range body.Jobs {
		if job.IsListed != nil && !*job.IsListed {
			continue
		}
		var allLocs []string
		if loc := trim(job.Location); loc != "" {
			allLocs = append(allLocs, loc)
		}
		for _, sl := range job.SecondaryLocations {
			if n := trim(sl.Location); n != "" {
				allLocs = append(allLocs, n)
			}
		}
		loc := strings.Join(allLocs, " · ")
		wt := job.WorkplaceType
		if wt == "" && job.IsRemote {
			wt = "remote"
		}
		applyURL := trim(job.ApplyURL)
		if applyURL == "" {
			applyURL = trim(job.JobURL)
		}
		var salary string
		if job.Compensation != nil {
			if len(job.Compensation.SummaryComponents) > 0 {
				sc := job.Compensation.SummaryComponents[0]
				salary = salaryRangeText(sc.CurrencyCode, sc.MinValue, sc.MaxValue, sc.Interval)
			}
			if salary == "" {
				salary = job.Compensation.TierSummary
			}
		}
		p := listing.Posting{
			URL:            job.JobURL,
			ApplyURL:       applyURL,
			Title:          job.Title,
			Company:        company,
			CompanyGuessed: guessed,
			Location:       loc,
			Tags:           []string{job.Department, job.Team},
			Fields:         setFields("employment_type", job.EmploymentType, "workplace_type", wt, "salary", salary),
			Text:           job.DescriptionHTML,
		}
		if pub, ok := parseDate(job.PublishedAt); ok {
			p.Published = pub
			p.PublishedBasis = "publishedAt"
		}
		if p, ok := finalise(p, ""); ok {
			postings = append(postings, p)
		}
	}
	return postings, nil
}

type ashbyResponse struct {
	Organization struct {
		Name string `json:"name"`
	} `json:"organization"`
	Jobs []ashbyJob `json:"jobs"`
}

type ashbySecondaryLoc struct {
	Location string `json:"location"`
}

type ashbyCompSummary struct {
	CurrencyCode string  `json:"currencyCode"`
	MinValue     float64 `json:"minValue"`
	MaxValue     float64 `json:"maxValue"`
	Interval     string  `json:"interval"`
}

type ashbyJob struct {
	ID                 string              `json:"id"`
	Title              string              `json:"title"`
	JobURL             string              `json:"jobUrl"`
	ApplyURL           string              `json:"applyUrl"`
	Location           string              `json:"location"`
	IsRemote           bool                `json:"isRemote"`
	IsListed           *bool               `json:"isListed"`
	WorkplaceType      string              `json:"workplaceType"`
	SecondaryLocations []ashbySecondaryLoc `json:"secondaryLocations"`
	Department         string              `json:"department"`
	Team               string              `json:"team"`
	EmploymentType     string              `json:"employmentType"`
	PublishedAt        string              `json:"publishedAt"`
	DescriptionHTML    string              `json:"descriptionHtml"`
	Compensation       *ashbyComp          `json:"compensation"`
}

type ashbyComp struct {
	TierSummary       string             `json:"compensationTierSummary"`
	SummaryComponents []ashbyCompSummary `json:"summaryComponents"`
}
