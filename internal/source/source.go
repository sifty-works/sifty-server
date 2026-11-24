package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var ErrNothingRecognised = errors.New("nothing recognised on the page")

type Attr struct{ Attr, Substr string }

type Site struct {
	Name        string `yaml:"-"`
	Address     string `yaml:"-"`
	ListAddress string `yaml:"list_address"`
	Content     []Attr `yaml:"content"`
	LinkPattern string `yaml:"link_pattern"`
	ScriptVar   string `yaml:"script_var"`
	SkipJSONLD  bool   `yaml:"skip_json_ld"`
}

type Module interface {
	Name() string
	List(ctx context.Context, f fetch.Fetcher, s Site) ([]listing.Posting, error)
}

type Matcher interface {
	Match(u *url.URL) bool
}

type Grouper interface {
	Group(u *url.URL) string
}

type Vacancier interface {
	Vacancy(u *url.URL) (string, bool)
}

type board struct {
	hosts []string
	depth int
	mid   string
}

func (b board) owns(u *url.URL) bool { return slices.Contains(b.hosts, strings.ToLower(u.Host)) }

func (b board) Vacancy(u *url.URL) (string, bool) {
	path := strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(u.Path, "/application"), "/apply"), "/")
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !b.owns(u) || len(segs) != b.depth || (b.mid != "" && segs[1] != b.mid) || slices.Contains(segs, "") {
		return "", false
	}
	canon, err := listing.Canonical(strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + path)
	return canon, err == nil
}

var apiModules = []Module{srcGreenhouse, srcLever, srcAshby, srcWorkable, srcHN}

func ConcreteATS(raw string) (string, bool) {
	if u, err := url.Parse(raw); err == nil {
		for _, m := range apiModules {
			if v, ok := m.(Vacancier); ok {
				if canon, ok := v.Vacancy(u); ok {
					return canon, true
				}
			}
		}
	}
	return "", false
}

func canonHost(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

func For(u *url.URL) (Module, Site) {
	s := Site{Address: u.String()}
	for _, m := range apiModules {
		if mm, ok := m.(Matcher); ok && mm.Match(u) {
			return m, s
		}
	}
	if entry, ok := sitesByHost[canonHost(u)]; ok {
		site := entry.site
		site.Address = s.Address
		return entry.mod, site
	}
	return srcGeneric, s
}

func GroupKey(u *url.URL) string {
	mod, _ := For(u)
	if g, ok := mod.(Grouper); ok {
		return g.Group(u)
	}
	return canonHost(u)
}

func fetchJSON(ctx context.Context, f fetch.Fetcher, apiURL string, dst any) error {
	resp, err := f.Get(ctx, apiURL, fetch.Opt{})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		return ErrNothingRecognised
	}
	return nil
}

func trim(s string) string { return strings.TrimSpace(s) }

func boardSlug(u *url.URL) string {
	p, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSuffix(u.Path, "/"), "/"), "/")
	return p
}

func isRoot(u *url.URL) bool {
	p := strings.TrimSuffix(u.Path, "/")
	if p == "" || p[0] != '/' {
		return false
	}
	return p[1:] != "" && !strings.Contains(p[1:], "/")
}

var dateFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseDate(s string) (time.Time, bool) {
	if s = trim(s); s == "" {
		return time.Time{}, false
	}
	for _, fmt := range dateFormats {
		if t, err := time.Parse(fmt, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func resolveURL(href, base string) string {
	if href == "" || base == "" {
		return href
	}
	hURL, err := url.Parse(href)
	if err != nil || hURL.IsAbs() {
		return href
	}
	if bURL, err := url.Parse(base); err == nil {
		return bURL.ResolveReference(hURL).String()
	}
	return href
}

func setFields(kvs ...string) map[string]string {
	var m map[string]string
	for i := 0; i+1 < len(kvs); i += 2 {
		if v := trim(kvs[i+1]); v != "" {
			if m == nil {
				m = make(map[string]string)
			}
			m[kvs[i]] = v
		}
	}
	return m
}

func salaryRangeText(currency string, min, max float64, period string) string {
	if min == 0 && max == 0 {
		return ""
	}
	per := map[string]string{
		"year": "/year", "1 year": "/year", "yearly": "/year", "per-year-salary": "/year",
		"month": "/month", "1 month": "/month",
		"hour": "/hour", "1 hour": "/hour",
		"day": "/day",
	}[strings.ToLower(trim(period))]
	var num string
	switch {
	case min > 0 && max > 0:
		num = fmt.Sprintf("%.0f–%.0f", min, max)
	case max > 0:
		num = fmt.Sprintf("%.0f", max)
	default:
		num = fmt.Sprintf("%.0f", min)
	}
	var parts []string
	if c := trim(currency); c != "" {
		parts = append(parts, c)
	}
	parts = append(parts, num)
	return strings.Join(parts, " ") + per
}

func finalise(p listing.Posting, base string) (listing.Posting, bool) {
	p.URL = resolveURL(trim(p.URL), base)
	p.ApplyURL = resolveURL(trim(p.ApplyURL), base)
	p.Title = trim(p.Title)
	p.Company = trim(p.Company)
	p.Location = trim(p.Location)
	p.Text = trim(p.Text)
	var tags []string
	for _, t := range p.Tags {
		if t = trim(t); t != "" {
			tags = append(tags, t)
		}
	}
	p.Tags = tags
	if p.Text != "" {
		p.TextState = listing.TextFull
	} else {
		p.TextState = listing.TextNotFetched
	}
	return p, p.Title != "" && p.URL != ""
}
