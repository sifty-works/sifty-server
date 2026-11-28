package locate

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/sifty-works/sifty-server/internal/fetch"
)

const (
	WayListing = "listing"
	WayBoard   = "board"
)

type Vacancy struct {
	ID       int64
	Title    string
	Company  string
	URL      string
	ApplyURL string
}

type Link struct {
	URL    string
	System string
	Way    string
}

type Job struct {
	Title   string
	Company string
	URL     string
}

type Boards interface {
	Jobs(ctx context.Context, system, board string) ([]Job, error)
	Posting(raw string) (system, address string, ok bool)
	Norm(s string) string
}

type Locator interface {
	Locate(ctx context.Context, v Vacancy, deep bool) (Link, error)
}

type Finder struct {
	Fetcher fetch.Fetcher
	Boards  Boards
}

var boardSystems = []string{"greenhouse", "lever", "ashby"}

var legalSuffixes = map[string]bool{
	"inc": true, "llc": true, "ltd": true, "gmbh": true, "ag": true, "corp": true, "corporation": true,
	"co": true, "bv": true, "sa": true, "plc": true, "limited": true, "oy": true, "ab": true,
	"srl": true, "sas": true, "pty": true, "incorporated": true,
}

var (
	boardName   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	embedToken  = regexp.MustCompile(`greenhouse\.io/embed/[^"'\s<>]*?[?&;]for=([A-Za-z0-9_-]+)`)
	boardToken  = regexp.MustCompile(`"boardToken"\s*:\s*"([A-Za-z0-9_-]+)"`)
	jobIDDigits = regexp.MustCompile(`^[0-9]+$`)
)

func (f Finder) Locate(ctx context.Context, v Vacancy, _ bool) (Link, error) {
	var first error
	for _, step := range []func(context.Context, Vacancy) (Link, error){f.fromListing, f.fromBoards} {
		link, err := step(ctx, v)
		if link.URL != "" {
			return link, nil
		}
		if first == nil {
			first = err
		}
	}
	return Link{}, first
}

func absent(err error) bool {
	return errors.Is(err, fetch.ErrNotFound) || errors.Is(err, fetch.ErrBlocked) || errors.Is(err, fetch.ErrRefused)
}

func (f Finder) fromListing(ctx context.Context, v Vacancy) (Link, error) {
	raw := cmp.Or(v.ApplyURL, v.URL)
	if system, address, ok := f.Boards.Posting(raw); ok {
		return Link{URL: address, System: system, Way: WayListing}, nil
	}
	resp, err := f.Fetcher.Get(ctx, raw, fetch.Opt{})
	if err != nil {
		if absent(err) {
			err = nil
		}
		return Link{}, err
	}
	final := cmp.Or(resp.URL, raw)
	if system, address, ok := f.Boards.Posting(final); ok {
		return Link{URL: address, System: system, Way: WayListing}, nil
	}
	u, err := url.Parse(final)
	if err != nil {
		return Link{}, nil
	}
	id := u.Query().Get("gh_jid")
	if !jobIDDigits.MatchString(id) {
		return Link{}, nil
	}
	for _, re := range []*regexp.Regexp{embedToken, boardToken} {
		if m := re.FindSubmatch(resp.Body); m != nil {
			return Link{URL: "https://boards.greenhouse.io/" + string(m[1]) + "/jobs/" + id, System: "greenhouse", Way: WayListing}, nil
		}
	}
	return Link{}, nil
}

func (f Finder) words(name string) []string {
	w := strings.Fields(f.Boards.Norm(name))
	for len(w) > 1 && legalSuffixes[w[len(w)-1]] {
		w = w[:len(w)-1]
	}
	return w
}

func (f Finder) key(name string) string {
	return strings.Join(f.words(name), "")
}

var titleSeparators = []string{" - ", " – ", " | ", " (", ", "}

func (f Finder) head(title string) string {
	cut := len(title)
	for _, sep := range titleSeparators {
		if i := strings.Index(title, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	return f.Boards.Norm(title[:cut])
}

func (f Finder) fromBoards(ctx context.Context, v Vacancy) (Link, error) {
	words := f.words(v.Company)
	company, title := f.key(v.Company), f.Boards.Norm(v.Title)
	if company == "" || title == "" {
		return Link{}, nil
	}
	var first error
	tried := map[string]bool{}
	for _, name := range []string{strings.Join(words, "-"), company} {
		if tried[name] || !boardName.MatchString(name) {
			continue
		}
		tried[name] = true
		for _, system := range boardSystems {
			jobs, err := f.Boards.Jobs(ctx, system, name)
			if err != nil {
				if !absent(err) && first == nil {
					first = err
				}
				continue
			}
			var loose []Job
			for _, j := range jobs {
				if f.key(j.Company) != company {
					continue
				}
				switch jt := f.Boards.Norm(j.Title); {
				case jt == title:
					return Link{URL: j.URL, System: system, Way: WayBoard}, nil
				case f.head(j.Title) == title || f.head(v.Title) == jt:
					loose = append(loose, j)
				}
			}
			if len(loose) == 1 {
				return Link{URL: loose[0].URL, System: system, Way: WayBoard}, nil
			}
			return Link{}, nil
		}
	}
	return Link{}, first
}

func Subject(vacancy int64) string { return strconv.FormatInt(vacancy, 10) }
