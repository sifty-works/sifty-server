package source

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

const braintrustHost = "app.usebraintrust.com"

var srcBraintrust Module = &braintrustSource{}

type braintrustSource struct{}

func (*braintrustSource) Name() string { return "braintrust" }

type btLocation struct {
	Location string `json:"location"`
	Country  string `json:"country"`
	City     string `json:"city"`
}

type btJob struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	Created     string       `json:"created"`
	PaymentType string       `json:"payment_type"`
	JobType     string       `json:"job_type"`
	Contract    string       `json:"contract_type"`
	BudgetMin   json.Number  `json:"budget_minimum_usd"`
	BudgetMax   json.Number  `json:"budget_maximum_usd"`
	Openings    *int         `json:"openings_left"`
	Locations   []btLocation `json:"locations"`
	Employer    struct {
		Name string `json:"name"`
	} `json:"employer"`
}

func (*braintrustSource) List(ctx context.Context, f fetch.Fetcher, s Site) ([]listing.Posting, error) {
	var body struct {
		Results []btJob `json:"results"`
	}
	if err := fetchJSON(ctx, f, s.ListAddress, &body); err != nil {
		return nil, err
	}
	var out []listing.Posting
	for _, j := range body.Results {
		if j.Employer.Name == "" || j.ID == 0 || (j.Openings != nil && *j.Openings == 0) {
			continue
		}
		var locs []string
		for _, l := range j.Locations {
			name := trim(l.Location)
			if name == "" {
				name = strings.Join(nonEmpty(l.City, l.Country), ", ")
			}
			if name != "" {
				locs = append(locs, name)
			}
		}
		min, _ := j.BudgetMin.Float64()
		max, _ := j.BudgetMax.Float64()
		period, paid := map[string]string{"hourly": "hour", "annual": "year", "monthly": "month"}[strings.ToLower(trim(j.PaymentType))]
		if !paid {
			min, max = 0, 0
		}
		empl := trim(j.JobType)
		if empl == "" {
			empl = j.Contract
		}
		p := listing.Posting{
			URL:      "https://" + braintrustHost + "/jobs/" + strconv.FormatInt(j.ID, 10) + "/",
			Title:    j.Title,
			Company:  j.Employer.Name,
			Location: strings.Join(locs, " · "),
			Fields:   setFields("salary", salaryRangeText("USD", min, max, period), "employment_type", empl),
		}
		if t, ok := parseDate(j.Created); ok {
			p.Published, p.PublishedBasis = t, "created"
		}
		if p, ok := finalise(p, ""); ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, ErrNothingRecognised
	}
	return out, nil
}

func (*braintrustSource) Detail(ctx context.Context, f fetch.Fetcher, p listing.Posting) (listing.Posting, error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return p, ErrNothingRecognised
	}
	id, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(u.Path, "/jobs/"), "/"), 10, 64)
	if err != nil {
		return p, ErrNothingRecognised
	}
	var d struct {
		Description  string `json:"description"`
		Requirements string `json:"requirements"`
		PublishedAt  string `json:"published_at"`
	}
	if err := fetchJSON(ctx, f, "https://"+braintrustHost+"/api/jobs/"+strconv.FormatInt(id, 10)+"/", &d); err != nil {
		return p, err
	}
	if text := trim(d.Description + "\n" + d.Requirements); text != "" {
		p.Text, p.TextState = text, listing.TextFull
	}
	if t, ok := parseDate(d.PublishedAt); ok {
		p.Published, p.PublishedBasis = t, "published_at"
	}
	return p, nil
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, s := range parts {
		if s = trim(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
