package source

import (
	"bytes"
	"context"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
	"strings"
)

type pageParser struct {
	name    string
	parse   func(body []byte, base string, s Site) ([]listing.Posting, bool)
	generic bool
	when    func(s Site, ct string, body []byte) bool
}

var pageParsers = []pageParser{
	{"feed", func(body []byte, base string, _ Site) ([]listing.Posting, bool) {
		ps, err := parseFeedXML(body, base)
		return ps, err == nil
	}, true, func(_ Site, ct string, body []byte) bool { return isXMLContent(ct, body) }},
	{"a16z", parseA16Z, true, func(s Site, _ string, _ []byte) bool { return s.ScriptVar != "" }},
	{"", func(body []byte, base string, _ Site) ([]listing.Posting, bool) {
		ps := parseJSONLD(body, base, false)
		return ps, len(ps) > 0
	}, true, func(s Site, _ string, _ []byte) bool { return !s.SkipJSONLD }},
	{"getro", parseGetro, true, nil},
	{"links", parseLinks, true, nil},
	{"paradigm", parseParadigm, false, nil},
}

type pageModule struct{ pageParser }

func (m *pageModule) Name() string { return m.name }
func (m *pageModule) List(ctx context.Context, f fetch.Fetcher, s Site) ([]listing.Posting, error) {
	body, _, base, err := fetchPage(ctx, f, s)
	if err != nil {
		return nil, err
	}
	if ps, _ := m.parse(body, base, s); len(ps) > 0 {
		return ps, nil
	}
	return nil, ErrNothingRecognised
}

func fetchPage(ctx context.Context, f fetch.Fetcher, s Site) (body []byte, ct, base string, err error) {
	u := s.ListAddress
	if u == "" {
		u = s.Address
	}
	r, err := f.Get(ctx, u, fetch.Opt{})
	if err != nil {
		return nil, "", "", err
	}
	base = r.URL
	if base == "" {
		base = u
	}
	return r.Body, r.ContentType, base, nil
}

var srcGeneric Module = &genericModule{}

type genericModule struct{}

func (g *genericModule) Name() string { return "generic" }
func (g *genericModule) List(ctx context.Context, f fetch.Fetcher, s Site) ([]listing.Posting, error) {
	body, ct, base, err := fetchPage(ctx, f, s)
	if err != nil {
		return nil, err
	}
	for _, p := range pageParsers {
		if p.generic && (p.when == nil || p.when(s, ct, body)) {
			if ps, ok := p.parse(body, base, s); ok {
				return ps, nil
			}
		}
	}
	return nil, ErrNothingRecognised
}

func isXMLContent(ct string, body []byte) bool {
	ct = strings.ToLower(ct)
	b := bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}))
	return strings.Contains(ct, "xml") || strings.Contains(ct, "rss") || strings.Contains(ct, "atom") ||
		bytes.HasPrefix(b, []byte("<?xml")) || bytes.HasPrefix(b, []byte("<rss")) || bytes.HasPrefix(b, []byte("<feed"))
}

func findScript(body []byte, marker string) string {
	s := string(body)
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(s[:i+1], "<script")
	if start < 0 {
		return ""
	}
	s = s[start:]
	j := strings.IndexByte(s, '>')
	k := strings.Index(s, "</script>")
	if j < 0 || k < j {
		return ""
	}
	return strings.TrimSpace(s[j+1 : k])
}
