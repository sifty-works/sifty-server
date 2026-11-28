package source

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var srcGreenhouse Module = &greenhouseSource{board{
	[]string{"boards.greenhouse.io", "job-boards.greenhouse.io", "boards.eu.greenhouse.io", "job-boards.eu.greenhouse.io"}, 3, "jobs"}}

type greenhouseSource struct{ board }

func (s *greenhouseSource) Name() string          { return "greenhouse" }
func (s *greenhouseSource) Match(u *url.URL) bool { return s.owns(u) && isRoot(u) }

func (s *greenhouseSource) Group(u *url.URL) string {
	return "boards-api." + strings.SplitN(strings.ToLower(u.Host), ".", 2)[1]
}

func (s *greenhouseSource) List(ctx context.Context, f fetch.Fetcher, cfg Site) ([]listing.Posting, error) {
	boardURL, err := url.Parse(cfg.Address)
	if err != nil {
		return nil, ErrNothingRecognised
	}
	board := boardSlug(boardURL)
	apiURL := "https://" + s.Group(boardURL) + "/v1/boards/" + board + "/jobs"
	if !cfg.TitlesOnly {
		apiURL += "?content=true"
	}
	var body ghResponse
	if err := fetchJSON(ctx, f, apiURL, &body); err != nil {
		return nil, err
	}
	boardCompany := trim(body.Company.Name)
	if boardCompany == "" {
		if boardCompany = trim(cfg.Name); boardCompany == "" {
			boardCompany = board
		}
	}
	var postings []listing.Posting
	for _, job := range body.Jobs {
		company := trim(job.CompanyName)
		companyGuessed := company == ""
		if company == "" {
			company = boardCompany
		}
		var tags []string
		for _, d := range job.Departments {
			tags = append(tags, d.Name)
		}
		p := listing.Posting{
			URL:            job.AbsoluteURL,
			ApplyURL:       fmt.Sprintf("https://boards.greenhouse.io/%s/jobs/%d", board, job.ID),
			Title:          job.Title,
			Company:        company,
			CompanyGuessed: companyGuessed,
			Location:       job.Location.Name,
			Tags:           tags,
			Text:           html.UnescapeString(job.Content),
		}
		if pub, ok := parseDate(job.FirstPublished); ok {
			p.Published = pub
			p.PublishedBasis = "first_published"
		}
		if p, ok := finalise(p, ""); ok {
			postings = append(postings, p)
		}
	}
	return postings, nil
}

type ghResponse struct {
	Company struct {
		Name string `json:"name"`
	} `json:"company"`
	Jobs []ghJob `json:"jobs"`
}

type ghJob struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	AbsoluteURL string `json:"absolute_url"`
	CompanyName string `json:"company_name"`
	Location    struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		Name string `json:"name"`
	} `json:"departments"`
	FirstPublished string `json:"first_published"`
	Content        string `json:"content"`
}
