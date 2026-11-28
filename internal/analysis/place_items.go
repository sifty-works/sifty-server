package analysis

import (
	"regexp"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/places"
)

type placeItem struct {
	label      string
	start, end int
}

var (
	itemSepRe       = regexp.MustCompile(`(?i)\s*(?:,|/|&|\bor\b|\band\b)\s*`)
	officeRe        = regexp.MustCompile(`(?i)\b(?:at|in|from|attend) (?:our|the) ((?-i:\p{Lu})[\p{L}.' ,-]{1,40}?) (?:offices?|hq|headquarters|campus)\b`)
	modeWordRe      = regexp.MustCompile(`(?i)\b(?:hybrid|remote|on-?site|in-office)\b`)
	parenRe         = regexp.MustCompile(`\(([^)]*)\)`)
	preferredPartRe = regexp.MustCompile(`(?i)\bpreferred\b`)
	placeMaxWords   = 6
	stateJoinGapRe  = regexp.MustCompile(`^\s*,\s*$`)
	itemTrimSpaceRe = regexp.MustCompile(`^\s+|\s+$`)
)

type token struct {
	label      string
	start, end int
	gapBefore  string
}

func tokens(text string) []token {
	var out []token
	at := 0
	gap := ""
	add := func(from, to int) {
		raw := text[from:to]
		if zoneCueRe.MatchString(raw) {
			return
		}
		label, lead := cleanPlace(raw)
		trimmed := itemTrimSpaceRe.ReplaceAllString(raw, "")
		offset := strings.Index(raw, trimmed)
		if label != "" {
			out = append(out, token{label: label, start: from + offset + lead, end: from + offset + lead + len(label), gapBefore: gap})
		}
	}
	compounds := places.Compounds(text)
	for _, loc := range itemSepRe.FindAllStringIndex(text, -1) {
		if slices.ContainsFunc(compounds, func(c []int) bool { return loc[0] >= c[0] && loc[1] <= c[1] }) {
			continue
		}
		add(at, loc[0])
		at, gap = loc[1], text[loc[0]:loc[1]]
	}
	add(at, len(text))
	return out
}

func blank(re *regexp.Regexp, text string) string {
	return re.ReplaceAllStringFunc(text, func(m string) string { return strings.Repeat(" ", len(m)) })
}

func placeItems(text string) []placeItem {
	toks := tokens(blank(modeWordRe, text))
	var out []placeItem
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if _, isMode := ParseMode(t.label); isMode || len(strings.Fields(t.label)) > placeMaxWords && !places.Recognised(t.label) || zoneWordRe.MatchString(t.label) {
			continue
		}
		if places.IsState(t.label) && len(t.label) == 2 && i > 0 && stateJoinGapRe.MatchString(t.gapBefore) {
			continue
		}
		if places.IsCity(t.label) && i+1 < len(toks) && stateJoinGapRe.MatchString(toks[i+1].gapBefore) {
			next := toks[i+1]
			if places.IsState(next.label) || places.Recognised(next.label) && !places.IsCity(next.label) && !places.HasCountries(t.label) {
				out = append(out, placeItem{text[t.start:next.end], t.start, next.end})
				i++
				continue
			}
		}
		if places.Recognised(t.label) {
			out = append(out, placeItem{t.label, t.start, t.end})
		}
	}
	return out
}

func recognisedPlace(label string) bool {
	if places.Recognised(label) {
		return true
	}
	i := strings.LastIndex(label, ",")
	return i > 0 && places.IsState(label[i+1:]) && places.Recognised(label[:i])
}

func segmentItems(seg string) []placeItem {
	outside := seg
	var out []placeItem
	for _, m := range parenRe.FindAllStringSubmatchIndex(seg, -1) {
		outside = outside[:m[0]] + strings.Repeat(" ", m[1]-m[0]) + outside[m[1]:]
		if preferredPartRe.MatchString(seg[m[2]:m[3]]) {
			continue
		}
		for _, it := range placeItems(seg[m[2]:m[3]]) {
			out = append(out, placeItem{it.label, m[2] + it.start, m[2] + it.end})
		}
	}
	return append(out, placeItems(outside)...)
}
