package source

import (
	"encoding/json"
	"strings"

	"github.com/sifty-works/sifty-server/internal/listing"
)

type a16zCompany struct {
	Jobs []a16zJob `json:"jobs"`
}

type a16zJob struct {
	Title         string      `json:"title"`
	URL           string      `json:"url"`
	CompanyName   string      `json:"companyName"`
	CompanyDomain string      `json:"companyDomain"`
	CreatedAt     string      `json:"createdAt"`
	Locations     []string    `json:"locations"`
	Remote        bool        `json:"remote"`
	Salary        *a16zSalary `json:"salary"`
}

type a16zSalary struct {
	Currency string  `json:"currency"`
	MinValue float64 `json:"minValue"`
	MaxValue float64 `json:"maxValue"`
	Period   string  `json:"period"`
}

func parseA16Z(body []byte, base string, s Site) ([]listing.Posting, bool) {
	content := findScript(body, s.ScriptVar)
	_, rest, ok := strings.Cut(content, s.ScriptVar)
	at := strings.IndexByte(rest, '[')
	var companies []a16zCompany
	if !ok || at < 0 || json.NewDecoder(strings.NewReader(rest[at:])).Decode(&companies) != nil {
		return nil, false
	}
	var jobs []a16zJob
	for _, c := range companies {
		jobs = append(jobs, c.Jobs...)
	}
	return a16zJobsToPostings(jobs, base)
}

func parseParadigm(body []byte, base string, _ Site) ([]listing.Posting, bool) {
	content := findScript(body, `jobs:[{`)
	idx := strings.Index(content, "jobs:")
	if idx < 0 {
		return nil, false
	}
	var jobs []a16zJob
	if json.NewDecoder(strings.NewReader(jsObjToJSON(content[idx+5:]))).Decode(&jobs) != nil {
		return nil, false
	}
	return a16zJobsToPostings(jobs, base)
}

func a16zJobsToPostings(jobs []a16zJob, base string) ([]listing.Posting, bool) {
	var out []listing.Posting
	for _, j := range jobs {
		wm := ""
		if j.Remote {
			wm = "remote"
		}
		p := listing.Posting{
			URL:      resolveURL(j.URL, base),
			Title:    j.Title,
			Company:  j.CompanyName,
			Location: strings.Join(j.Locations, " · "),
			Fields:   setFields("workplace_type", wm, "salary", a16zSalaryText(j.Salary)),
		}
		if t, ok := parseDate(j.CreatedAt); ok {
			p.Published, p.PublishedBasis = t, "createdAt"
		}
		if p, ok := finalise(p, base); ok {
			out = append(out, p)
		}
	}
	return out, len(out) > 0
}

func a16zSalaryText(s *a16zSalary) string {
	if s == nil {
		return ""
	}
	return salaryRangeText(s.Currency, s.MinValue, s.MaxValue, s.Period)
}

func jsObjToJSON(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				out.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
			j := i + 1
			for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= '0' && s[j] <= '9') || s[j] == '_') {
				j++
			}
			k := j
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			if k < len(s) && s[k] == ':' {
				out.WriteByte('"')
				out.WriteString(s[i:j])
				out.WriteByte('"')
			} else {
				out.WriteString(s[i:j])
			}
			i = j - 1
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}
