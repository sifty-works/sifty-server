package candidate

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DerivedYears struct {
	Total       int
	Leading     int
	Engineering int
}

var (
	leadingTitleRe     = regexp.MustCompile(`(?i)\b(cto|ceo|coo|cio|vp|vice[- ]president|head|director|lead|manager|chief|founder|co-?founder)\b`)
	engineeringTitleRe = regexp.MustCompile(`(?i)\b(engineer|engineers|engineering|developer|architect|programmer)\b`)
	isoMonthRe         = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})(?:[-/.]\d{1,2})?$`)
	yearRe             = regexp.MustCompile(`^(\d{4})$`)
)

func monthIndex(s string, isEnd bool) (int, bool) {
	s = strings.TrimSpace(s)
	year, month := 0, 0
	switch {
	case isoMonthRe.MatchString(s):
		m := isoMonthRe.FindStringSubmatch(s)
		year, _ = strconv.Atoi(m[1])
		month, _ = strconv.Atoi(m[2])
	case yearRe.MatchString(s):
		year, _ = strconv.Atoi(s)
		month = 1
		if isEnd {
			month = 12
		}
	}
	if year < 1950 || month < 1 || month > 12 {
		return 0, false
	}
	return year*12 + month - 1, true
}

type monthSpan struct{ from, to int }

func expSpan(e Experience, now time.Time) (monthSpan, bool) {
	from, ok := monthIndex(e.Start, false)
	if !ok {
		return monthSpan{}, false
	}
	var to int
	if e.Current || strings.TrimSpace(e.End) == "" {
		if !e.Current {
			return monthSpan{}, false
		}
		to = now.Year()*12 + int(now.Month()) - 1
	} else if to, ok = monthIndex(e.End, true); !ok {
		return monthSpan{}, false
	}
	if to < from {
		return monthSpan{}, false
	}
	return monthSpan{from, to}, true
}

func coveredMonths(spans []monthSpan) int {
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	total, end := 0, -1
	for _, s := range spans {
		from := s.from
		if from <= end {
			from = end + 1
		}
		if s.to >= from {
			total += s.to - from + 1
		}
		if s.to > end {
			end = s.to
		}
	}
	return total
}

func DeriveYears(exp []Experience, now time.Time) DerivedYears {
	var all, lead, eng []monthSpan
	for _, e := range exp {
		title := strings.TrimSpace(e.Title)
		span, dated := expSpan(e, now)
		if dated {
			all = append(all, span)
		}
		if leadingTitleRe.MatchString(title) && dated {
			lead = append(lead, span)
		}
		if engineeringTitleRe.MatchString(title) && dated {
			eng = append(eng, span)
		}
	}
	return DerivedYears{
		Total:       coveredMonths(all) / 12,
		Leading:     coveredMonths(lead) / 12,
		Engineering: coveredMonths(eng) / 12,
	}
}

func EndedBefore(e Experience, t time.Time) bool {
	span, ok := expSpan(e, t)
	return ok && span.to < t.Year()*12+int(t.Month())-1
}

func CountedMonths(exp []Experience, now time.Time) int {
	var spans []monthSpan
	undated := 0
	for _, e := range exp {
		if span, ok := expSpan(e, now); ok {
			spans = append(spans, span)
		} else {
			undated++
		}
	}
	return coveredMonths(spans) + undated*12
}
