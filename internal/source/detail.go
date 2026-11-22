package source

import (
	"bytes"
	"context"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
	xhtml "golang.org/x/net/html"
	"strings"
)

func extractBodyText(body []byte, attrs []Attr) string {
	doc, _ := xhtml.Parse(bytes.NewReader(body))
	inner := func(n *xhtml.Node) string {
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			_ = xhtml.Render(&b, c)
		}
		return b.String()
	}
	if len(attrs) > 0 {
		var blocks []string
		seen := map[*xhtml.Node]bool{}
		for _, a := range attrs {
			attr := strings.ToLower(a.Attr)
			htmlWalk(doc, func(n *xhtml.Node) bool {
				if n.Type != xhtml.ElementNode || seen[n] {
					return true
				}
				for _, na := range n.Attr {
					if strings.ToLower(na.Key) == attr && strings.Contains(na.Val, a.Substr) {
						seen[n] = true
						blocks = append(blocks, inner(n))
					}
				}
				return true
			})
		}
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n")
		}
	}
	for _, tag := range []string{"main", "article", "body"} {
		var found *xhtml.Node
		htmlWalk(doc, func(n *xhtml.Node) bool {
			if found == nil && n.Type == xhtml.ElementNode && n.Data == tag {
				found = n
				return false
			}
			return found == nil
		})
		if found != nil {
			return inner(found)
		}
	}
	return ""
}

func companyFromPageTitle(body []byte) string {
	doc, _ := xhtml.Parse(bytes.NewReader(body))
	var text string
	htmlWalk(doc, func(n *xhtml.Node) bool {
		if text != "" {
			return false
		}
		if n.Type == xhtml.ElementNode && n.Data == "title" && n.FirstChild != nil {
			var sb strings.Builder
			htmlWalk(n, func(c *xhtml.Node) bool {
				if c.Type == xhtml.TextNode {
					sb.WriteString(c.Data)
				}
				return true
			})
			text = strings.Join(strings.Fields(sb.String()), " ")
			return false
		}
		return true
	})
	if text == "" {
		return ""
	}
	for _, sep := range []string{" | ", " - "} {
		if i := strings.Index(text, sep); i >= 0 {
			text = text[:i]
		}
	}
	text = strings.TrimSpace(text)
	if i := strings.LastIndex(text, " at "); i >= 0 {
		return strings.TrimSpace(text[i+4:])
	}
	return ""
}

func htmlWalk(n *xhtml.Node, fn func(*xhtml.Node) bool) {
	if n != nil && fn(n) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			htmlWalk(c, fn)
		}
	}
}

func firstText(n *xhtml.Node, pred func(*xhtml.Node) bool) string {
	var out string
	htmlWalk(n, func(c *xhtml.Node) bool {
		if out != "" {
			return false
		}
		if !pred(c) {
			return true
		}
		var sb strings.Builder
		htmlWalk(c, func(t *xhtml.Node) bool {
			if t.Type == xhtml.TextNode {
				sb.WriteString(t.Data)
			}
			return true
		})
		out = strings.Join(strings.Fields(sb.String()), " ")
		return false
	})
	return out
}

func Detail(ctx context.Context, f fetch.Fetcher, p listing.Posting, s Site) (listing.Posting, error) {
	resp, err := f.Get(ctx, p.URL, fetch.Opt{})
	if err != nil {
		return p, err
	}
	base := resp.URL
	if base == "" {
		base = p.URL
	}
	useBody := s.SkipJSONLD
	tryPageTitle := false
	if cs := parseJSONLD(resp.Body, base, true); len(cs) > 0 {
		if m, ok, ambig := matchJSONLD(cs, p); ok {
			if s.SkipJSONLD {
				m.Text = ""
			}
			p = mergeDetail(p, m)
		} else {
			tryPageTitle = !ambig
		}
	} else {
		useBody = true
		tryPageTitle = true
	}
	if useBody {
		if raw := extractBodyText(resp.Body, s.Content); raw != "" {
			p.Text = raw
			p.TextState = listing.TextFull
		}
	}
	if tryPageTitle && p.Company == "" {
		if co := companyFromPageTitle(resp.Body); co != "" {
			p.Company = co
		}
	}
	if p.ApplyURL == "" {
		doc, _ := xhtml.Parse(bytes.NewReader(resp.Body))
		htmlWalk(doc, func(n *xhtml.Node) bool {
			if p.ApplyURL != "" || n.Type != xhtml.ElementNode || n.Data != "a" {
				return p.ApplyURL == ""
			}
			for _, a := range n.Attr {
				if a.Key == "href" {
					if u, ok := ConcreteATS(resolveURL(a.Val, base)); ok {
						p.ApplyURL = u
						return false
					}
				}
			}
			return true
		})
	}
	return p, nil
}

func mergeDetail(p, m listing.Posting) listing.Posting {
	if m.Text != "" {
		p.Text = m.Text
		p.TextState = listing.TextFull
	}
	for _, kv := range [][2]*string{{&p.Company, &m.Company}, {&p.Location, &m.Location}} {
		if *kv[0] == "" {
			*kv[0] = *kv[1]
		}
	}
	if p.Published.IsZero() && !m.Published.IsZero() {
		p.Published, p.PublishedBasis = m.Published, m.PublishedBasis
	}
	if p.Expires.IsZero() && !m.Expires.IsZero() {
		p.Expires = m.Expires
	}
	for k, v := range m.Fields {
		if p.Fields == nil {
			p.Fields = make(map[string]string)
		}
		if p.Fields[k] == "" {
			p.Fields[k] = v
		}
	}
	return p
}
