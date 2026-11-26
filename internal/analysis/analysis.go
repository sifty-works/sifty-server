package analysis

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const Kind = "analyze_vacancy"

type Section struct {
	Name       string
	Start, End int
}

type Doc struct {
	Title, Company, Location string
	Tags                     []string
	Fields                   map[string]string
	Body                     string
	URL, ApplyURL            string
	Origin                   facts.Origin
	Original                 string
	Sections                 []Section
	Headings                 []Span
	Facts                    []facts.Fact
}

type Worker interface {
	Name() string
	Version() int
	Produces() []facts.Kind
	Extract(ctx context.Context, doc *Doc) ([]facts.Fact, error)
}

var Workers = []Worker{siteFields{}, conditions{}, requirements{}, offer{}}

type Loader func(ctx context.Context, id int64) (doc *Doc, version int64, ok bool, err error)

func Subject(id, version int64) string { return fmt.Sprintf("%d/%d", id, version) }

func ParseSubject(s string) (id, version int64, err error) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return 0, 0, fmt.Errorf("analysis: subject %q: expected <id>/<version>", s)
	}
	if id, err = strconv.ParseInt(a, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("analysis: subject %q: %w", s, err)
	}
	if version, err = strconv.ParseInt(b, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("analysis: subject %q: %w", s, err)
	}
	return id, version, nil
}

func Job(database *db.DB, load Loader) func(ctx context.Context, j *runner.Job) error {
	return func(ctx context.Context, j *runner.Job) error {
		id, version, err := ParseSubject(j.Subject)
		if err != nil {
			return runner.Permanent(err)
		}
		return Analyse(ctx, database, load, id, version)
	}
}

func Analyse(ctx context.Context, database *db.DB, load Loader, id, version int64) error {
	doc, current, ok, err := load(ctx, id)
	if err != nil || !ok || current != version {
		return err
	}
	doc.Origin = originOf(doc.URL, doc.ApplyURL)
	doc.Sections, doc.Headings = Split(doc)
	var all []facts.Fact
	for _, w := range Workers {
		found, err := w.Extract(ctx, doc)
		if err != nil {
			return fmt.Errorf("analysis: %s on vacancy %d: %w", w.Name(), id, err)
		}
		for _, f := range found {
			f.Vacancy, f.Version, f.Worker, f.WorkerVersion = id, version, w.Name(), w.Version()
			f.Source.Origin = doc.Origin
			doc.Facts = append(doc.Facts, f)
			all = append(all, f)
		}
	}
	chosen := effective(all)
	for _, w := range Workers {
		var mine []facts.Fact
		for _, f := range chosen {
			if f.Worker == w.Name() {
				mine = append(mine, f)
			}
		}
		marker := facts.Fact{Kind: facts.Analysed, Value: digest(mine), Source: facts.Source{Way: facts.Vocabulary, Origin: doc.Origin}, Worker: w.Name(), WorkerVersion: w.Version()}
		if err := facts.Save(ctx, database, id, version, w.Name(), append(mine, marker)); err != nil {
			return err
		}
	}
	return nil
}

func digest(fs []facts.Fact) string {
	h := fnv.New64a()
	for _, f := range fs {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x01", f.Kind, f.Value, f.Flag, f.Start, f.End, f.Source.Way, f.Source.Origin)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

func effective(all []facts.Fact) []facts.Fact {
	inText, siteFact := map[facts.Kind]bool{}, map[facts.Kind]bool{}
	for _, f := range all {
		inText[f.Kind] = inText[f.Kind] || f.End > f.Start
		siteFact[f.Kind] = siteFact[f.Kind] || f.Source.Way == facts.SiteField
	}
	var out []facts.Fact
	for _, f := range all {
		fieldOnly := f.Source.Way == facts.SiteField && f.End == f.Start
		switch {
		case f.Source.Origin == facts.Aggregator && fieldOnly && inText[f.Kind]:
		case f.Source.Origin == facts.Employer && f.Source.Way == facts.Vocabulary && siteFact[f.Kind]:
		default:
			out = append(out, f)
		}
	}
	return out
}

type Line struct {
	Text       string
	Start, End int
}

func textLines(s string) []Line {
	var out []Line
	for at := 0; at < len(s); {
		end := strings.IndexByte(s[at:], '\n')
		if end < 0 {
			end = len(s) - at
		}
		raw := s[at : at+end]
		trimmed := strings.TrimLeft(raw, " \t\r")
		start := at + len(raw) - len(trimmed)
		trimmed = strings.TrimRight(trimmed, " \t\r")
		if trimmed != "" {
			out = append(out, Line{Text: trimmed, Start: start, End: start + len(trimmed)})
		}
		at += end + 1
	}
	return out
}

func (d *Doc) sectionAt(offset int) string {
	for _, s := range d.Sections {
		if offset >= s.Start && offset < s.End {
			return s.Name
		}
	}
	return SectionIntro
}

func (d *Doc) isHeading(l Line) bool {
	for _, h := range d.Headings {
		if l.Start < h.End && h.Start < l.End {
			return true
		}
	}
	return false
}
