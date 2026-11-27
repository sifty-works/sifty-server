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
	"github.com/sifty-works/sifty-server/internal/skills"
)

const (
	maxRequirementLines  = 40
	maxRequirementLength = 300
	minRequirementLength = 4

	minPlainHeadingWords  = 2
	maxPlainHeadingWords  = 6
	maxLongParagraphWords = 25
)

var (
	preferredRe      = regexp.MustCompile(`(?i)\b(?:preferred|bonus|plus|ideally|nice to have|nice-to-have|desirable|advantage|beneficial|even better|not (?:strictly )?(?:required|essential|necessary))\b`)
	requiredRe       = regexp.MustCompile(`(?i)\b(?:must|required|mandatory|essential|you(?:'ll| will) need)\b`)
	notReqRe         = regexp.MustCompile(`(?i)\bnot (?:strictly )?(?:required|essential|necessary)\b`)
	bulletRe         = regexp.MustCompile(`^(?:[-*•·–—▪●◦]|\d{1,2}[.)])\s+`)
	sentenceRe       = regexp.MustCompile(`[.!?]\s+`)
	gluedSentenceRe  = regexp.MustCompile(`[a-z]{2}[.!?][A-Z]`)
	aboutCompanyRe   = regexp.MustCompile(`(?i)^(?:we\b|our\b|this\s+(?:role|position|job)\b)`)
	thisLeadRe       = regexp.MustCompile(`(?i)^this\b`)
	demandRe         = regexp.MustCompile(`(?i)\b(?:want|need|looking for|seeking|require|expect)`)
	eligibleLineRe   = regexp.MustCompile(`(?i)\b(?:` + alt(append(slices.Clone(vocab.Engagement["authorization_words"]), vocab.Place["eligibility_cues"]...)) + `)\b`)
	fallbackRe       = regexp.MustCompile(`(?i)\b(?:experience|knowledge|proficien\w*|familiar\w*|years|degree|bachelor|master|skilled|understanding|ability to|fluen\w*|expertise)\b`)
	offerWordRe      = regexp.MustCompile(`(?i)\b(?:salary|benefits|equity|vacation|insurance|perks|we offer|stipend)\b`)
	nonRequirementRe = regexp.MustCompile(`(?i)\b(?:(?:competitive|attractive|generous|excellent) (?:salary|compensation|pay|benefits|package)|base salary|(?:salary|pay|compensation) (?:range|band|package|details)|total (?:starting )?compensation|(?:salary|compensation|pay|bonus|equity) (?:and|&) (?:equity|bonus|benefits|stock)|benefits package|we offer|perks|stipend|beware of (?:recruitment )?scams?|fraudulent (?:job )?offers?|recruitment fraud|official channels|pto|vacation (?:policy|days|time)|unlimited (?:vacation|paid)|paid (?:time off|leave|vacation|holidays?)|(?:parental|maternity|paternity) leave|fertility|family planning|(?:health|dental|vision|medical)(?:\s*(?:,|and|&)\s*(?:health|dental|vision|medical))*\s+(?:insurance|benefits|coverage|plans?)|(?:medical|health)[, ]+(?:and )?(?:dental|vision)|dental[, ]+(?:and )?vision|insurance (?:coverage|plans?)|401\s?\(?k|stock options|equity (?:grants?|package|options)|reasonable accommodations?|request (?:an? )?accommodations?|to apply|when applying|how to apply|apply (?:by|online|now|today|(?:right )?here|via|through|using)|(?:application|hiring|interview) process|(?:submit|submitting) (?:a|an|your) (?:job )?application|if you (?:have a disability|need an? accommodation)|please let us know|inclusive (?:environment|workplace|culture)|we strive|join (?:us|our team)|best and brightest)\b`)
	inlineBulletRe   = regexp.MustCompile(`(?:^|\s)[•·▪●◦]\s*`)
	inlineDashRe     = regexp.MustCompile(`(?:^|\s)-\s+`)
	hashtagRe        = regexp.MustCompile(`^(?:#[\p{L}\p{N}_-]+\s*)+$`)
)

type requirements struct{}

func (requirements) Name() string { return "requirement_lines" }

func (requirements) Version() int { return derivedVersion(10) }

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
	company := strings.ToLower(doc.Company)
	aboutCompany := func(text string, fallback, strict bool) bool {
		lower := strings.ToLower(text)
		if company != "" && strings.HasPrefix(lower, "at "+company+",") {
			return true
		}
		about := aboutCompanyRe.MatchString(text)
		lead := about || company != "" && strings.HasPrefix(lower, company)
		return fallback && thisLeadRe.MatchString(text) && !about || (fallback || strict) && lead && !demandRe.MatchString(text) && !eligibleLineRe.MatchString(text)
	}
	lines := doc.lines()
	firstEnd, blockStart, inline, headed := 0, -1, false, false
	for i, line := range lines {
		if strings.HasSuffix(line.Text, ":") {
			continue
		}
		within, _ := doc.sectionOf(line.Start)
		section := within.Name
		inSection := section == SectionRequirements || section == SectionNiceToHave
		later := false
		if inSection {
			block, opened := blockOf(doc, within, line.Start)
			if block.Start != blockStart {
				blockStart, firstEnd, headed = block.Start, line.End, false
				inline = !strings.Contains(doc.Original[block.Start:line.Start], "\n")
			}
			last := lastItemEnd(items, block)
			if last < 0 && !opened && !headed && line.End > firstEnd && plainHeading(doc, items, lines, i, within) {
				headed = true
				continue
			}
			later = last >= 0 && line.Start >= last || last < 0 && (opened || headed || inline && line.Start >= firstEnd)
		}
		base := facts.Flag("")
		fallback := false
		switch {
		case section == SectionRequirements:
			base = facts.Required
		case section == SectionNiceToHave:
			base = facts.Preferred
		case !hasSection && (section == SectionIntro || section == SectionRole || section == SectionOther) && !offerWordRe.MatchString(line.Text):
			base = facts.Required
			fallback = true
		}
		if base == "" {
			continue
		}
		lines := []Line{line}
		if inSection {
			lines = splitBullets(line)
		}
		for _, l := range lines {
			for _, piece := range splitLong(l, fallback) {
				reads := fallbackRe.MatchString(piece.Text) || later && len(skills.Groups(piece.Text)) > 0
				if (fallback || later) && !reads {
					continue
				}
				if !nonRequirementRe.MatchString(piece.Text) && !eeoRe.MatchString(piece.Text) && !hashtagRe.MatchString(piece.Text) && !aboutCompany(piece.Text, fallback, later || inSection && !inItem(items, line)) {
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

func plainHeading(doc *Doc, items []Span, lines []Line, i int, within Section) bool {
	if !shortPlain(lines[i], items) || fallbackRe.MatchString(lines[i].Text) || len(skills.Groups(lines[i].Text)) > 0 {
		return false
	}
	if i+1 >= len(lines) || lines[i+1].Start >= within.End {
		return true
	}
	next := lines[i+1]
	if slices.ContainsFunc(doc.Headings, func(h Span) bool { return h.Start >= lines[i].End && h.End <= next.Start }) {
		return false
	}
	return shortPlain(next, items) || len(strings.Fields(next.Text)) > maxLongParagraphWords
}

func inItem(items []Span, l Line) bool {
	return slices.ContainsFunc(items, func(s Span) bool { return l.Start >= s.Start && l.End <= s.End })
}

func shortPlain(l Line, items []Span) bool {
	words := len(strings.Fields(l.Text))
	return words >= minPlainHeadingWords && words <= maxPlainHeadingWords && !strings.ContainsAny(l.Text[len(l.Text)-1:], ".!?:;") && !inItem(items, l)
}

func blockOf(doc *Doc, within Section, offset int) (Section, bool) {
	block, opened := within, false
	for _, h := range doc.Headings {
		if h.Start <= within.Start || h.Start >= within.End {
			continue
		}
		if h.Start > offset {
			block.End = h.Start
			break
		}
		block.Start, opened = h.Start, true
	}
	return block, opened
}

func lastItemEnd(items []Span, within Section) int {
	last := -1
	for _, i := range items {
		if i.Start >= within.Start && i.End <= within.End {
			last = max(last, i.End)
		}
	}
	return last
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

func splitLong(l Line, always bool) []Line {
	trim := func(l Line) Line {
		if m := bulletRe.FindString(l.Text); m != "" {
			l.Start += len(m)
			l.Text = l.Text[len(m):]
		}
		return l
	}
	l = trim(l)
	if len(l.Text) <= maxRequirementLength && !always {
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
