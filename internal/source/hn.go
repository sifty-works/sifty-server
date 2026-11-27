package source

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var srcHN Module = &hnSource{}

type hnSource struct{}

func (s *hnSource) Name() string { return "hn" }
func (s *hnSource) Match(u *url.URL) bool {
	return strings.ToLower(u.Host) == "news.ycombinator.com"
}

type hnSearchHit struct{ ObjectID, Title string }
type hnSearchResult struct{ Hits []hnSearchHit }
type hnComment struct {
	ID        int64
	Text      string
	CreatedAt string `json:"created_at"`
	Children  []*hnComment
}

func (s *hnSource) List(ctx context.Context, f fetch.Fetcher, cfg Site) ([]listing.Posting, error) {
	var sr hnSearchResult
	if err := fetchJSON(ctx, f, "https://hn.algolia.com/api/v1/search_by_date?tags=story,author_whoishiring&hitsPerPage=10", &sr); err != nil {
		return nil, err
	}
	var storyIDs []string
	for _, h := range sr.Hits {
		if strings.HasPrefix(h.Title, "Ask HN: Who is hiring?") && len(storyIDs) < 2 {
			storyIDs = append(storyIDs, h.ObjectID)
		}
	}
	if len(storyIDs) == 0 {
		return nil, ErrNothingRecognised
	}
	var out []listing.Posting
	for i, id := range storyIDs {
		var item hnComment
		if err := fetchJSON(ctx, f, "https://hn.algolia.com/api/v1/items/"+id, &item); err != nil {
			if i == 0 {
				return nil, err
			}
			continue
		}
		for _, c := range item.Children {
			if p, ok := hnCommentToPosting(c); ok {
				if p, ok = finalise(p, cfg.Address); ok {
					out = append(out, p)
				}
			}
		}
	}
	return out, nil
}

var (
	hnWorkRe  = regexp.MustCompile(`(?i)\b(?:(?:part[- ]?)?remote|on[- ]?site|hybrid|in[- ]office|in[- ]person)\b`)
	hnEmpRe   = regexp.MustCompile(`(?i)\b(?:full[- ]?time|part[- ]?time|contract|freelance|intern(?:ship)?s?|temporary)\b`)
	hnJoinRe  = regexp.MustCompile(`(?i)[\s/,&+–—-]|\bor\b|\band\b|\([^)]*\)`)
	hnLeadRe  = regexp.MustCompile(`(?i)^(?:in|or|and|at)\b\s*`)
	hnSchedRe = regexp.MustCompile(`(?i)\bdays?\b`)
	hnURLRe   = regexp.MustCompile(`https?://\S*`)
	hnDomain  = regexp.MustCompile(`^[^\s.]+(?:\.[^\s.]+)+/?$`)
	hnRoleRe  = regexp.MustCompile(`(?i)\b(?:engineer|engineering|eng|swe|developer|designer|manager|scientist|analyst|lead|head|director|architect|researcher|specialist|consultant|administrator|recruiter|sourcer|marketer|writer|officer|executive|founder|builder|technologist|representative|tester|sales|intern|sre|devops|cto|ceo|coo|cfo|pm|ae|role|position|opening|job)s?\b`)
)

func hnBare(s string) bool {
	return strings.TrimSpace(hnJoinRe.ReplaceAllString(hnEmpRe.ReplaceAllString(hnWorkRe.ReplaceAllString(s, ""), ""), "")) == ""
}

func hnPlaceText(s string) string {
	s = strings.NewReplacer("(", " ", ")", " ").Replace(hnWorkRe.ReplaceAllString(s, ""))
	s = strings.TrimFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(strings.Fields(hnLeadRe.ReplaceAllString(s, "")), " ")
}

func hnCommentToPosting(c *hnComment) (listing.Posting, bool) {
	firstLine, _, _ := strings.Cut(c.Text, "<p>")
	firstLine = listing.PlainText(firstLine)
	parts := strings.Split(firstLine, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if i > 0 {
			parts[i] = strings.TrimSpace(hnURLRe.ReplaceAllString(parts[i], ""))
		}
	}
	company := parts[0]
	if len(parts) < 2 || company == "" {
		return listing.Posting{}, false
	}
	var title, loc, workType, employType, salary string
	var rest []string
	for _, pt := range parts[1:] {
		work, emp := hnWorkRe.FindAllString(pt, -1), hnEmpRe.FindAllString(pt, -1)
		_, inner := hnParenthetical(pt)
		lead := strings.TrimLeftFunc(pt, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		startsWork := hnWorkRe.FindStringIndex(lead) != nil && hnWorkRe.FindStringIndex(lead)[0] == 0
		switch {
		case pt == "" || hnDomain.MatchString(pt):
		case strings.ContainsAny(pt, "$£€"):
			if salary == "" {
				salary = pt
			}
		case len(work)+len(emp) > 0 && hnBare(pt):
			if inner != "" && loc == "" {
				loc = inner
			}
		case hnRoleRe.MatchString(pt):
			if title == "" {
				title = pt
			}
			emp = nil
		case startsWork || (len(work) > 0 && (hnSchedRe.MatchString(pt) || (inner != "" && hnWorkRe.MatchString(inner) && hnBare(inner)))):
			t := hnPlaceText(pt)
			if hnSchedRe.MatchString(pt) {
				t = pt
			}
			if loc == "" {
				loc = t
			}
		default:
			rest = append(rest, pt)
			continue
		}
		if workType == "" && len(work) > 0 {
			workType = strings.Join(work, " / ")
		}
		if employType == "" && len(emp) > 0 {
			employType = strings.Join(emp, " / ")
		}
	}
	if title == "" {
		title = "Open roles"
	}
	if loc == "" && len(rest) > 0 {
		loc = rest[0]
	}
	p := listing.Posting{
		URL:     fmt.Sprintf("https://news.ycombinator.com/item?id=%d", c.ID),
		Company: company, Title: title, Location: loc,
		Text:   c.Text,
		Fields: setFields("workplace_type", workType, "employment_type", employType, "salary", salary),
	}
	if t, ok := parseDate(c.CreatedAt); ok {
		p.Published, p.PublishedBasis = t, "created_at"
	}
	return p, true
}

func hnParenthetical(s string) (string, string) {
	i := strings.Index(s, "(")
	j := strings.Index(s, ")")
	if i > 0 && j > i {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1 : j])
	}
	return s, ""
}
