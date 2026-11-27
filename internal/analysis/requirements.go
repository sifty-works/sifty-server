package analysis

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/sifty-works/sifty-server/internal/facts"
)

const (
	maxRequirementLines  = 40
	maxRequirementLength = 300
	minRequirementLength = 4
)

var (
	preferredRe      = regexp.MustCompile(`(?i)\b(?:preferred|bonus|plus|ideally|nice to have|nice-to-have|desirable|advantage|beneficial|even better|not (?:strictly )?(?:required|essential|necessary))\b`)
	requiredRe       = regexp.MustCompile(`(?i)\b(?:must|required|mandatory|essential|you(?:'ll| will) need)\b`)
	notReqRe         = regexp.MustCompile(`(?i)\bnot (?:strictly )?(?:required|essential|necessary)\b`)
	bulletRe         = regexp.MustCompile(`^(?:[-*•·–—▪●◦]|\d{1,2}[.)])\s+`)
	sentenceRe       = regexp.MustCompile(`[.!?]\s+`)
	gluedSentenceRe  = regexp.MustCompile(`[a-z]{2}[.!?][A-Z]`)
	fallbackRe       = regexp.MustCompile(`(?i)\b(?:experience|knowledge|proficien\w*|familiar\w*|years|degree|bachelor|master|skilled|understanding|ability to|fluen\w*|expertise)\b`)
	offerWordRe      = regexp.MustCompile(`(?i)\b(?:salary|benefits|equity|vacation|insurance|perks|we offer|stipend)\b`)
	nonRequirementRe = regexp.MustCompile(`(?i)\b(?:(?:competitive|attractive|generous|excellent) (?:salary|compensation|pay|benefits|package)|base salary|(?:salary|pay|compensation) (?:range|band|package|details)|total (?:starting )?compensation|(?:salary|compensation|pay|bonus|equity) (?:and|&) (?:equity|bonus|benefits|stock)|benefits package|we offer|perks|stipend|pto|vacation (?:policy|days|time)|unlimited (?:vacation|paid)|paid (?:time off|leave|vacation|holidays?)|(?:parental|maternity|paternity) leave|fertility|family planning|(?:health|dental|vision|medical)(?:\s*(?:,|and|&)\s*(?:health|dental|vision|medical))*\s+(?:insurance|benefits|coverage|plans?)|(?:medical|health)[, ]+(?:and )?(?:dental|vision)|dental[, ]+(?:and )?vision|insurance (?:coverage|plans?)|401\s?\(?k|stock options|equity (?:grants?|package|options)|reasonable accommodations?|request (?:an? )?accommodations?|to apply|when applying|how to apply|apply (?:by|online|now|today|here|via|through|using)|(?:application|hiring|interview) process|(?:submit|submitting) (?:a|an|your) (?:job )?application|if you (?:have a disability|need an? accommodation)|please let us know|inclusive (?:environment|workplace|culture)|we strive|join (?:us|our team)|best and brightest)\b`)
	inlineBulletRe   = regexp.MustCompile(`(?:^|\s)[•·▪●◦]\s*`)
	inlineDashRe     = regexp.MustCompile(`(?:^|\s)-\s+`)
	hashtagRe        = regexp.MustCompile(`^(?:#[\p{L}\p{N}_-]+\s*)+$`)
)

type requirements struct{}

func (requirements) Name() string { return "requirement_lines" }

func (requirements) Version() int { return 3 }

func (requirements) Produces() []facts.Kind { return []facts.Kind{facts.Requirement} }

func (requirements) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	items := listItems(doc)
	out := requirementLines(doc, items, hasRequirementSection(doc))
	if len(out) == 0 {
		out = requirementLines(doc, items, false)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Flag == facts.Required && out[j].Flag != facts.Required })
	if len(out) > maxRequirementLines {
		out = out[:maxRequirementLines]
	}
	return out, nil
}

func requirementLines(doc *Doc, items []Span, hasSection bool) []facts.Fact {
	var out []facts.Fact
	companyLead := ""
	if doc.Company != "" {
		companyLead = "at " + strings.ToLower(doc.Company) + ","
	}
	for _, line := range doc.lines() {
		if strings.HasSuffix(line.Text, ":") {
			continue
		}
		within, _ := doc.sectionOf(line.Start)
		section := within.Name
		inSection := section == SectionRequirements || section == SectionNiceToHave
		if inSection && afterLastItem(items, within, line) {
			continue
		}
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
		lines := []Line{line}
		if inSection {
			lines = splitBullets(line)
		}
		for _, l := range lines {
			for _, piece := range splitLong(l) {
				if !nonRequirementRe.MatchString(piece.Text) && !eeoRe.MatchString(piece.Text) && !hashtagRe.MatchString(piece.Text) &&
					(companyLead == "" || !strings.HasPrefix(strings.ToLower(piece.Text), companyLead)) {
					out = append(out, requirementFact(piece, base))
				}
			}
		}
	}
	return out
}

func listItems(doc *Doc) []Span {
	nodes, err := xhtml.ParseFragment(strings.NewReader(doc.Body), &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return nil
	}
	var out []Span
	cursor := 0
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.DataAtom == atom.Li {
			for _, l := range paragraphLines(n) {
				if start, ok := locateLine(doc.Original, cursor, l.text, true); ok {
					cursor = start + len(l.text)
					out = append(out, Span{start, cursor})
				}
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func afterLastItem(items []Span, within Section, line Line) bool {
	last := -1
	for _, i := range items {
		if i.Start >= within.Start && i.End <= within.End {
			last = max(last, i.End)
		}
	}
	return last >= 0 && line.Start >= last
}

func splitBullets(l Line) []Line {
	marks := inlineBulletRe
	if strings.HasPrefix(l.Text, "- ") {
		marks = inlineDashRe
	}
	locs := marks.FindAllStringIndex(l.Text, -1)
	if len(locs) == 0 {
		return []Line{l}
	}
	var out []Line
	cut := func(from, to int) {
		if t := strings.TrimSpace(l.Text[from:to]); t != "" {
			start := l.Start + from + strings.Index(l.Text[from:to], t)
			out = append(out, Line{Text: t, Start: start, End: start + len(t)})
		}
	}
	at := 0
	for _, loc := range locs {
		cut(at, loc[0])
		at = loc[1]
	}
	cut(at, len(l.Text))
	return out
}

func hasRequirementSection(doc *Doc) bool {
	return slices.ContainsFunc(doc.Sections, func(s Section) bool { return s.Name == SectionRequirements || s.Name == SectionNiceToHave })
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
	var cuts [][2]int
	for _, loc := range sentenceRe.FindAllStringIndex(l.Text, -1) {
		cuts = append(cuts, [2]int{loc[0] + 1, loc[1]})
	}
	for _, loc := range gluedSentenceRe.FindAllStringIndex(l.Text, -1) {
		cuts = append(cuts, [2]int{loc[0] + 3, loc[1] - 1})
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] < cuts[j][0] })
	for _, cut := range cuts {
		parts = append(parts, Line{Text: strings.TrimSpace(l.Text[at:cut[0]]), Start: l.Start + at, End: l.Start + cut[0]})
		at = cut[1]
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
