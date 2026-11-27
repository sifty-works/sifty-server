package analysis

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/skills"
)

const (
	offered = "offered"
	refused = "refused"
	none    = "none"
)

var (
	monthNumber       = monthNumbers()
	deadlineRe        = deadlineCue()
	lengthRes         = lengthPatterns()
	clearanceRes      = clearancePatterns()
	relocationRes     = relocationPatterns()
	relocationNoiseRe = regexp.MustCompile(`(?i)^\s+(?:` + alt(keyVocab.RelocationNoise) + `)\b`)
	sponsorRe         = regexp.MustCompile(`(?i)\bsponsor(?:s|ed|ing|ship)?\b`)
	visaWordsRe       = regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.VisaWords) + `)\b`)
	visaSupportRe     = regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.VisaWords) + `)\s+(?:\w+\s+)?(?:` + alt(keyVocab.VisaSupport) + `)\b`)
	formRe            = regexp.MustCompile(`(?i)^\W*(?:` + alt(keyVocab.FormStarts) + `)\b|` + altRe(keyVocab.FormAsks))
	lengthNoiseRe     = regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.LengthNoise) + `)\b`)
	bareSponsorRe     = regexp.MustCompile(`(?i)^\s*(?:$|[.,;:!)(]|(?:` + alt(keyVocab.SponsorObjects) + `)\b)`)
	denialRe          = regexp.MustCompile(`(?i)(?:\b(?:` + alt(keyVocab.DenialWords) + `)\b|n['’]t\b)[^.;!?]{0,60}$`)
	denialAfterRe     = regexp.MustCompile(`(?i)^\s*(?:(?:is|are|will be)\s+)?[:–-]?\s*(?:` + alt(keyVocab.DenialWords) + `)\b`)
	contrastRe        = regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.ContrastWords) + `)\b`)
)

func monthNumbers() map[string]int {
	out := map[string]int{}
	for _, m := range keyVocab.Months {
		n, _ := strconv.Atoi(m.Name)
		for _, p := range m.Patterns {
			out[p] = n
		}
	}
	return out
}

const (
	dashes     = `\-‐‑‒–—`
	lengthUnit = `(months?|weeks?|years?)`
)

var lengthSpan = func() string {
	num := `(?:` + skills.NumberPattern + `|\d+(?:\.\d+)?)`
	return `(` + num + `)(?:\s*(?:[` + dashes + `]|to)\s*(` + num + `))?\+?`
}()

func deadlineCue() *regexp.Regexp {
	var months []string
	for p := range monthNumber {
		months = append(months, p)
	}
	sort.Slice(months, func(i, j int) bool { return len(months[i]) > len(months[j]) })
	month := `(` + alt(months) + `)\b`
	date := `(\d{4})-(\d{2})-(\d{2})|(\d{1,2})(?:st|nd|rd|th)?\s+(?:of\s+)?` + month + `\.?(?:,?\s+(\d{4})\b)?|` + month + `\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b(?:,?\s+(\d{4})\b)?`
	return regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.DeadlineCues) + `)\b[\s:,–-]*(?:(?:on|by|is|the|before|until)\s+)*(?:(?:` + alt(keyVocab.Weekdays) + `)\.?,?\s+)?(?:` + date + `)`)
}

type lengthPattern struct {
	re         *regexp.Regexp
	checkAfter bool
}

func lengthPatterns() []lengthPattern {
	return []lengthPattern{
		{regexp.MustCompile(`(?i)\b` + lengthSpan + `[\s` + dashes + `]\s*` + lengthUnit + `\s+(?:` + alt(keyVocab.ContractNouns) + `)\b`), true},
		{regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.ContractLabels) + `)\s*(?:of|for|is|:|-|–|,)?\s*(?:(?:` + alt(keyVocab.LengthQualifiers) + `)\s+)?` + lengthSpan + `\s*` + lengthUnit + `\b`), false},
	}
}

type clearancePattern struct {
	re    *regexp.Regexp
	value string
}

func clearancePatterns() []clearancePattern {
	country := `(?-i:(?:U\.?S\.?A?|UK|EU|[A-Z][\p{L}-]+))`
	out := []clearancePattern{
		{regexp.MustCompile(`(?i)\b((?:(?:` + alt(keyVocab.ClearanceLeads) + `)\s+)*(?:(?:` + alt(keyVocab.ClearanceLevels) + `)\s+(?:(?:level|security)\s+)?)?clearance)\b`), ""},
		{regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.CitizenshipCues) + `)\s+(?:an?\s+)?(` + country + `\s+(?:citizens?|nationals?))\b`), ""},
		{regexp.MustCompile(`(?i)\b(` + country + `\s+(?:citizenship|citizens?|nationals?)\s+(?:(?:is|are)\s+)?(?:` + alt(keyVocab.CitizenshipRequired) + `))\b`), ""},
	}
	for _, t := range keyVocab.ClearanceTerms {
		out = append(out, clearancePattern{regexp.MustCompile(`(?i)\b(?:` + altRe(t.Patterns) + `)\b`), t.Name})
	}
	return out
}

func relocationPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brelocation\s+(?:(?:and|&|\+|/)\s*\w+\s+)?(?:(?:is|are|will be)\s+)?(?:not\s+)?(?:` + alt(keyVocab.RelocationSupport) + `)\b`),
		regexp.MustCompile(`(?i)\b(?:` + alt(keyVocab.RelocationLeads) + `)\s+(?:\w+\s+){0,4}?relocation\b`),
	}
}

func sentenceFact(kind facts.Kind, value string, line Line, at int) facts.Fact {
	sentence := sentenceAround(line.Text, at)
	start := max(strings.Index(line.Text, sentence), 0)
	return vocabFact(kind, value, "", line.Start+start, line.Start+start+len(sentence))
}

func bodyLines(doc *Doc) []Line {
	var out []Line
	for _, line := range textLines(doc.Original) {
		if !doc.isHeading(line) {
			out = append(out, line)
		}
	}
	return out
}

func asksApplicant(text string, loc []int) bool {
	start, end := 0, len(text)
	for _, b := range sentenceRe.FindAllStringIndex(text, -1) {
		if b[1] <= loc[0] {
			start = b[1]
		} else if b[0] >= loc[1] {
			end = b[0] + 1
			break
		}
	}
	return strings.HasSuffix(text[start:end], "?") || formRe.MatchString(text[start:end])
}

func denied(text string, loc []int) bool {
	before := text[:loc[0]]
	if c := contrastRe.FindAllStringIndex(before, -1); len(c) > 0 {
		before = before[c[len(c)-1][1]:]
	}
	return denialRe.MatchString(before+text[loc[0]:loc[1]]) || denialAfterRe.MatchString(text[loc[1]:])
}

func deadlineOf(doc *Doc) (facts.Fact, bool) {
	now := doc.FirstSeen
	for _, line := range bodyLines(doc) {
		for _, m := range deadlineRe.FindAllStringSubmatch(line.Text, -1) {
			at := strings.Index(line.Text, m[0])
			var day, month, year int
			switch {
			case m[1] != "":
				year, month, day = atoi(m[1]), atoi(m[2]), atoi(m[3])
			case m[4] != "":
				day, month, year = atoi(m[4]), monthNumber[strings.ToLower(m[5])], atoi(m[6])
			default:
				month, day, year = monthNumber[strings.ToLower(m[7])], atoi(m[8]), atoi(m[9])
			}
			if year == 0 && now.IsZero() {
				continue
			}
			if t, ok := resolveDate(day, month, year, now); ok {
				return sentenceFact(facts.Deadline, t.Format("2006-01-02"), line, at), true
			}
		}
	}
	return facts.Fact{}, false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func resolveDate(day, month, year int, now time.Time) (time.Time, bool) {
	var best time.Time
	var gap time.Duration
	years := []int{year}
	if year == 0 {
		years = []int{now.Year() - 1, now.Year(), now.Year() + 1}
	}
	for _, y := range years {
		t := time.Date(y, time.Month(month), day, 0, 0, 0, 0, now.Location())
		if t.Day() != day || int(t.Month()) != month {
			continue
		}
		if d := t.Sub(now).Abs(); best.IsZero() || d < gap {
			best, gap = t, d
		}
	}
	return best, !best.IsZero()
}

func countOf(s string) string {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	n, _ := skills.NumberOf(s)
	return strconv.Itoa(n)
}

func contractLengthOf(doc *Doc) (facts.Fact, bool) {
	for _, line := range bodyLines(doc) {
		for _, p := range lengthRes {
			for _, m := range p.re.FindAllStringSubmatchIndex(line.Text, -1) {
				before, after := line.Text[:m[0]], line.Text[m[1]:]
				if smartRe.MatchString(before) || negationRe.MatchString(before) || partyRe.MatchString(before) || p.checkAfter && contractNoiseRe.MatchString(after) || lengthNoiseRe.MatchString(sentenceAround(line.Text, m[0])) {
					continue
				}
				count, unit := countOf(line.Text[m[2]:m[3]]), strings.TrimSuffix(strings.ToLower(line.Text[m[6]:m[7]]), "s")
				if m[4] >= 0 {
					count += "–" + countOf(line.Text[m[4]:m[5]])
				}
				if count != "1" {
					unit += "s"
				}
				return sentenceFact(facts.ContractLen, count+" "+unit, line, m[0]), true
			}
		}
	}
	return facts.Fact{}, false
}

func clearanceOf(doc *Doc) (facts.Fact, bool) {
	type hit struct {
		at    int
		value string
		line  Line
		local int
	}
	var hits []hit
	for _, line := range bodyLines(doc) {
		if eeoRe.MatchString(line.Text) {
			continue
		}
		for _, p := range clearanceRes {
			for _, m := range p.re.FindAllStringSubmatchIndex(line.Text, -1) {
				value := p.value
				if len(m) > 2 {
					value = strings.Join(strings.Fields(line.Text[m[2]:m[3]]), " ")
				}
				if len(strings.Fields(value)) > 1 && !denied(line.Text, m[:2]) {
					hits = append(hits, hit{line.Start + m[0], value, line, m[0]})
				}
			}
		}
	}
	if len(hits) == 0 {
		return facts.Fact{}, false
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var values []string
	for _, h := range hits {
		if !slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(v, h.value) }) {
			values = append(values, h.value)
		}
	}
	return sentenceFact(facts.Clearance, strings.Join(values, ", "), hits[0].line, hits[0].local), true
}

func relocationOf(doc *Doc) (facts.Fact, bool) {
	for _, line := range bodyLines(doc) {
		for _, re := range relocationRes {
			for _, m := range re.FindAllStringIndex(line.Text, -1) {
				if partyRe.MatchString(line.Text[:m[0]]) || relocationNoiseRe.MatchString(line.Text[m[1]:]) {
					continue
				}
				return sentenceFact(facts.Relocation, map[bool]string{true: none, false: offered}[denied(line.Text, m)], line, m[0]), true
			}
		}
	}
	return facts.Fact{}, false
}

func visaOf(doc *Doc) (facts.Fact, bool) {
	for _, line := range bodyLines(doc) {
		for _, re := range []*regexp.Regexp{sponsorRe, visaSupportRe} {
			for _, m := range re.FindAllStringIndex(line.Text, -1) {
				if asksApplicant(line.Text, m) {
					continue
				}
				no := denied(line.Text, m)
				switch {
				case re == visaSupportRe || visaWordsRe.MatchString(sentenceAround(line.Text, m[0])):
					return sentenceFact(facts.Visa, map[bool]string{true: refused, false: offered}[no], line, m[0]), true
				case no && bareSponsorRe.MatchString(line.Text[m[1]:]):
					return sentenceFact(facts.Visa, refused, line, m[0]), true
				}
			}
		}
	}
	return facts.Fact{}, false
}
