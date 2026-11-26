package analysis

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/sifty-works/sifty-server/internal/facts"
)

const (
	maxRequirementLines  = 40
	maxRequirementLength = 300
	minRequirementLength = 4
)

var (
	preferredRe = regexp.MustCompile(`(?i)\b(?:preferred|bonus|plus|ideally|nice to have|nice-to-have|desirable|advantage|beneficial|even better|not (?:strictly )?(?:required|essential|necessary))\b`)
	requiredRe  = regexp.MustCompile(`(?i)\b(?:must|required|mandatory|essential|you(?:'ll| will) need)\b`)
	notReqRe    = regexp.MustCompile(`(?i)\bnot (?:strictly )?(?:required|essential|necessary)\b`)
	bulletRe    = regexp.MustCompile(`^(?:[-*•·–—▪●◦]|\d{1,2}[.)])\s+`)
	sentenceRe  = regexp.MustCompile(`[.!?]\s+`)
	fallbackRe  = regexp.MustCompile(`(?i)\b(?:experience|knowledge|proficien\w*|familiar\w*|years|degree|bachelor|master|skilled|understanding|ability to|fluen\w*|expertise)\b`)
	offerWordRe = regexp.MustCompile(`(?i)\b(?:salary|benefits|equity|vacation|insurance|perks|we offer|stipend)\b`)
)

type requirements struct{}

func (requirements) Name() string { return "requirement_lines" }

func (requirements) Version() int { return 2 }

func (requirements) Produces() []facts.Kind { return []facts.Kind{facts.Requirement} }

func (requirements) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	hasSection := false
	for _, s := range doc.Sections {
		hasSection = hasSection || s.Name == SectionRequirements || s.Name == SectionNiceToHave
	}
	var out []facts.Fact
	for _, line := range textLines(doc.Original) {
		if doc.isHeading(line) || strings.HasSuffix(line.Text, ":") {
			continue
		}
		section := doc.sectionAt(line.Start)
		base := facts.Flag("")
		switch {
		case section == SectionRequirements:
			base = facts.Required
		case section == SectionNiceToHave:
			base = facts.Preferred
		case !hasSection && (section == SectionIntro || section == SectionRole || section == SectionOther) &&
			fallbackRe.MatchString(line.Text) && !offerWordRe.MatchString(line.Text):
			base = facts.Required
		}
		if base == "" {
			continue
		}
		for _, part := range splitLong(line) {
			out = append(out, requirementFact(part, base))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Flag == facts.Required && out[j].Flag != facts.Required })
	if len(out) > maxRequirementLines {
		out = out[:maxRequirementLines]
	}
	return out, nil
}

func splitLong(l Line) []Line {
	trim := func(l Line) Line {
		if m := bulletRe.FindString(l.Text); m != "" {
			l.Start += len(m)
			l.Text = l.Text[len(m):]
		}
		return l
	}
	l = trim(l)
	if len(l.Text) <= maxRequirementLength {
		return keepLong([]Line{l})
	}
	var parts []Line
	at := 0
	for _, loc := range sentenceRe.FindAllStringIndex(l.Text, -1) {
		parts = append(parts, Line{Text: strings.TrimSpace(l.Text[at : loc[0]+1]), Start: l.Start + at, End: l.Start + loc[0] + 1})
		at = loc[1]
	}
	parts = append(parts, Line{Text: l.Text[at:], Start: l.Start + at, End: l.End})
	return keepLong(parts)
}

func keepLong(ls []Line) []Line {
	var out []Line
	for _, l := range ls {
		if n := len(l.Text); n >= minRequirementLength && n <= maxRequirementLength {
			out = append(out, l)
		}
	}
	return out
}

func requirementFact(l Line, base facts.Flag) facts.Fact {
	flag := base
	required := requiredRe.MatchString(notReqRe.ReplaceAllString(l.Text, " "))
	switch {
	case required:
		flag = facts.Required
	case preferredRe.MatchString(l.Text):
		flag = facts.Preferred
	}
	return vocabFact(facts.Requirement, l.Text, flag, l.Start, l.End)
}
