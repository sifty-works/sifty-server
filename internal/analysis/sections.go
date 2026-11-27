package analysis

import (
	_ "embed"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"gopkg.in/yaml.v3"
)

const (
	SectionIntro        = "intro"
	SectionOther        = "other"
	SectionRole         = "role"
	SectionRequirements = "requirements"
	SectionNiceToHave   = "nice_to_have"
	SectionOffer        = "offer"
	SectionCompany      = "company"
	SectionProcess      = "process"
)

const (
	maxHeadingWords = 10
	maxColonWords   = 8
	lineLevel       = 5
)

var (
	labelValueRe = regexp.MustCompile(`^\s*[\p{Sc}\d]`)
)

//go:embed sections.yaml
var sectionsYAML []byte

type headingEntry struct{ Text, Kind string }

var headingMap = func() struct{ Headings, Contains []headingEntry } {
	var v struct{ Headings, Contains []headingEntry }
	if err := yaml.Unmarshal(sectionsYAML, &v); err != nil {
		panic("analysis: load sections.yaml: " + err.Error())
	}
	return v
}()

type heading struct {
	text  string
	level int
	kind  string
	line  string
	at    int
}

const (
	minGluedHeadingLength = 8
	maxGluedHeadingLength = 80
	maxInlineWords        = 8
)

func normHeading(s string) string {
	s = strings.ToLower(strings.NewReplacer("’", "'", "‘", "'", "&", " and ").Replace(s))
	s = strings.TrimFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(strings.Fields(s), " ")
}

func companyHeading(norm, company string) bool {
	name := normHeading(company)
	return len(name) >= 2 && (norm == "about "+name || norm == "who is "+name || norm == "life at "+name || norm == "why "+name)
}

func classify(norm, company string, exact bool) string {
	for _, e := range headingMap.Headings {
		if norm == e.Text {
			return e.Kind
		}
	}
	if companyHeading(norm, company) {
		return SectionCompany
	}
	if exact || len(strings.Fields(norm)) > maxHeadingWords {
		return ""
	}
	for _, e := range headingMap.Contains {
		if strings.Contains(norm, e.Text) {
			return e.Kind
		}
	}
	return ""
}

type htmlLine struct {
	text string
	bold bool
}

func paragraphLines(p *xhtml.Node) []htmlLine {
	var lines []htmlLine
	var b strings.Builder
	words, boldWords := 0, 0
	flush := func() {
		if t := strings.TrimSpace(b.String()); t != "" {
			lines = append(lines, htmlLine{text: t, bold: words == boldWords})
		}
		b.Reset()
		words, boldWords = 0, 0
	}
	var walk func(n *xhtml.Node, bold bool)
	walk = func(n *xhtml.Node, bold bool) {
		switch {
		case n.Type == xhtml.TextNode:
			b.WriteString(n.Data)
			w := len(strings.Fields(n.Data))
			words += w
			if bold {
				boldWords += w
			}
		case n.Type == xhtml.ElementNode && n.DataAtom == atom.Br:
			flush()
		case n.Type == xhtml.ElementNode:
			inner := bold || n.DataAtom == atom.Strong || n.DataAtom == atom.Em || n.DataAtom == atom.B
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inner)
			}
		}
	}
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		walk(c, false)
	}
	flush()
	return lines
}

func findHeadings(body, company string) []heading {
	nodes, err := xhtml.ParseFragment(strings.NewReader(body), &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return nil
	}
	var out []heading
	add := func(text string, level int, marked bool) {
		text = html.UnescapeString(strings.TrimSpace(text))
		kind := classify(normHeading(text), company, level == lineLevel && !marked)
		if marked || kind != "" {
			out = append(out, heading{text: text, level: level, kind: kind})
		}
	}
	for _, n := range nodes {
		if n.Type != xhtml.ElementNode {
			continue
		}
		switch n.DataAtom {
		case atom.H1, atom.H2:
			add(nodeText(n), 2, true)
		case atom.H3:
			add(nodeText(n), 3, true)
		case atom.H4, atom.H5, atom.H6:
			add(nodeText(n), 4, true)
		case atom.P:
			for _, l := range paragraphLines(n) {
				if hs := gluedHeadings(l.text, company); len(hs) > 0 {
					out = append(out, hs...)
					continue
				}
				short := len(strings.Fields(l.text)) <= maxHeadingWords
				colon := strings.HasSuffix(l.text, ":") && len(strings.Fields(l.text)) <= maxColonWords
				if short {
					add(l.text, lineLevel, l.bold || colon)
				}
			}
		}
	}
	return out
}

func gluedHeadings(line, company string) []heading {
	var out []heading
	prev := ' '
	for i, r := range line {
		if i > maxGluedHeadingLength {
			break
		}
		if i >= minGluedHeadingLength && unicode.IsUpper(r) && unicode.IsLower(prev) {
			if kind := inlineKind(line[:i], company); kind != "" {
				return []heading{{text: line[:i], level: lineLevel, kind: kind, line: line}}
			}
		}
		prev = r
	}
	for from := 0; from < len(line); {
		at, end, kind := inlineHeading(line, from, company)
		if kind == "" {
			break
		}
		out = append(out, heading{text: line[at:end], level: lineLevel, kind: kind, line: line, at: at})
		from = end
	}
	return out
}

func inlineKind(text, company string) string {
	if kind := classify(normHeading(text), company, true); kind != SectionOther {
		return kind
	}
	return ""
}

func inlineHeading(line string, from int, company string) (int, int, string) {
	for p := from; p < len(line); p++ {
		r, _ := utf8.DecodeRuneInString(line[p:])
		if !unicode.IsLetter(r) || p > 0 && !unicode.IsUpper(r) {
			continue
		}
		prior := strings.TrimRight(line[:p], " \t")
		spaced := p == 0 || strings.ContainsRune(" \t.!?", rune(line[p-1]))
		if !spaced && !unicode.IsLower(rune(line[p-1])) {
			continue
		}
		afterEnd := p > 0 && prior != "" && strings.ContainsRune(".!?", rune(prior[len(prior)-1]))
		var ends []int
		for i := p; i < len(line) && len(ends) < maxInlineWords; i++ {
			if i+1 == len(line) || strings.ContainsRune(" \t:;", rune(line[i+1])) {
				ends = append(ends, i+1)
			}
		}
		for k := len(ends); k >= 1; k-- {
			text := strings.TrimRight(line[p:ends[k-1]], " \t:;-–—•·▪●◦")
			end := p + len(text)
			last, _ := utf8.DecodeLastRuneInString(text)
			kind := inlineKind(text, company)
			if text == "" || kind == "" || !unicode.IsLetter(last) && !unicode.IsDigit(last) {
				continue
			}
			rest := strings.TrimLeft(line[end:], " \t:;")
			rr, _ := utf8.DecodeRuneInString(rest)
			next := rest != "" && (unicode.IsUpper(rr) || unicode.IsDigit(rr) || strings.ContainsRune("•·▪●◦-", rr))
			words := strings.Fields(text)
			firstLetter, _ := utf8.DecodeRuneInString(words[len(words)-1])
			var ok bool
			switch {
			case end < len(line) && line[end] == ':':
				ok = rest != "" || p > 0
			case !spaced:
			case p == 0:
				ok = next
			case afterEnd:
				ok = rest == "" || next
			default:
				ok = k >= 2 && next && unicode.IsUpper(firstLetter)
			}
			if kind == SectionOffer && labelValueRe.MatchString(rest) {
				ok = false
			}
			if ok {
				return p, end, kind
			}
		}
	}
	return 0, 0, ""
}

func nodeText(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func locateLine(text string, from int, line string, whole bool) (int, bool) {
	for at := 0; ; {
		i := strings.Index(text[from+at:], line)
		if i < 0 {
			return 0, false
		}
		start := from + at + i
		end := start + len(line)
		if whole {
			if strings.TrimSpace(text[strings.LastIndexByte(text[:start], '\n')+1:start]) == "" && strings.TrimSpace(text[end:end+lineLength(text[end:])]) == "" {
				return start, true
			}
		} else if (start == 0 || text[start-1] == '\n' || text[start-1] == ' ' || text[start-1] == '\t') && (end == len(text) || text[end] == '\n' || text[end] == ' ' || text[end] == '\r') {
			return start, true
		}
		at += i + 1
	}
}

func lineLength(s string) int {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return i
	}
	return len(s)
}

func Split(doc *Doc) ([]Section, []Span) {
	text := doc.Original
	var located []struct {
		Span
		heading
	}
	cursor, lineAt, lastLine := 0, 0, ""
	for _, h := range findHeadings(doc.Body, doc.Company) {
		needle := h.text
		if h.line != "" {
			needle = h.line
		}
		start := lineAt
		if h.line == "" || h.line != lastLine {
			var ok bool
			if start, ok = locateLine(text, cursor, needle, false); !ok {
				continue
			}
			lineAt, lastLine = start, h.line
		}
		start += h.at
		cursor = start + len(h.text)
		located = append(located, struct {
			Span
			heading
		}{Span{start, cursor}, h})
	}
	var sections []Section
	var spans []Span
	open := Section{Name: SectionIntro}
	level := 0
	closeAt := func(end int) {
		if end > open.Start {
			open.End = end
			sections = append(sections, open)
		}
	}
	for _, h := range located {
		spans = append(spans, h.Span)
		inherits := h.kind == "" && open.Name != SectionIntro && (h.level == lineLevel || h.level > level)
		if inherits {
			continue
		}
		kind := h.kind
		if kind == "" {
			kind = SectionOther
		}
		closeAt(h.Start)
		open, level = Section{Name: kind, Start: h.Start}, h.level
	}
	closeAt(len(text))
	return sections, spans
}
