package source

import (
	"bytes"
	"github.com/sifty-works/sifty-server/internal/listing"
	xhtml "golang.org/x/net/html"
	"net/url"
	"regexp"
	"strings"
)

var vacancyPathRE = regexp.MustCompile(`(?i)/(?:job|jobs|career|position)/[^/?]+`)

func parseLinks(body []byte, base string, s Site) ([]listing.Posting, bool) {
	var linkRE *regexp.Regexp
	if s.LinkPattern != "" {
		linkRE, _ = regexp.Compile(s.LinkPattern)
	}
	doc, _ := xhtml.Parse(bytes.NewReader(body))
	seen := map[string]bool{}
	keyToIdx := map[string]int{}
	var out []listing.Posting
	htmlWalk(doc, func(n *xhtml.Node) bool {
		if n.Type != xhtml.ElementNode || n.Data != "a" {
			return true
		}
		var href string
		for _, a := range n.Attr {
			if a.Key == "href" {
				href = a.Val
				break
			}
		}
		abs := resolveURL(href, base)
		if abs == "" {
			return true
		}
		u, err := url.Parse(abs)
		if err != nil {
			return true
		}
		u.RawQuery, u.Fragment = "", ""
		key := u.String()
		isVacancy := linkRE != nil && linkRE.MatchString(u.Path)
		if !isVacancy {
			_, isVacancy = ConcreteATS(u.String())
		}
		if !isVacancy {
			isVacancy = vacancyPathRE.MatchString(u.Path)
		}
		if !isVacancy {
			return true
		}
		switch strings.ToLower(u.Path[strings.LastIndex(u.Path, "/")+1:]) {
		case "signup", "login", "search", "new", "rss", "feed":
			return true
		}
		if seen[key] {
			if idx, ok := keyToIdx[key]; ok && out[idx].Company == "" {
				if co := firstText(n, func(c *xhtml.Node) bool {
					return c.Type == xhtml.ElementNode && c.Data == "h3"
				}); co != "" {
					out[idx].Company = co
				}
			}
			return true
		}
		seen[key] = true
		var title, company string
		for _, a := range n.Attr {
			if a.Key == "aria-label" {
				sv := strings.Join(strings.Fields(a.Val), " ")
				if i := strings.Index(sv, " – "); i >= 0 {
					company, title = sv[:i], sv[i+len(" – "):]
				} else {
					title = sv
				}
				break
			}
		}
		if title == "" {
			title = firstText(n, func(c *xhtml.Node) bool {
				return c.Type == xhtml.ElementNode && (c.Data == "h2" || c.Data == "h3" || c.Data == "h4")
			})
		}
		if title == "" {
			title = firstText(n, func(c *xhtml.Node) bool {
				return c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) != ""
			})
		}
		lt := strings.ToLower(title)
		if lt == "" || strings.HasPrefix(lt, "apply") || strings.HasPrefix(lt, "view") ||
			strings.HasPrefix(lt, "see all") || strings.HasPrefix(lt, "learn") ||
			strings.HasPrefix(lt, "next") || strings.HasPrefix(lt, "previous") {
			return true
		}
		keyToIdx[key] = len(out)
		out = append(out, listing.Posting{URL: key, Title: title, Company: company, TextState: listing.TextNotFetched})
		return true
	})
	return out, len(out) > 0
}
