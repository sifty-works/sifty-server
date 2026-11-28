package answer

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/text"
)

//go:embed phrasings.yaml
var phrasingsYAML []byte

var vocab = func() (v struct {
	phrases     map[string]string
	contains    map[string][]string
	leads       []string
	consent     []string
	sensitive   map[string]bool
	drafted     map[string]bool
	ethnicity   []string
	raceOptions []string
}) {
	var raw struct {
		Leads            []string
		Consent          []string
		Sensitive        []string
		Drafted          []string
		EthnicityOptions []string `yaml:"ethnicity_options"`
		RaceOptions      []string `yaml:"race_options"`
		Contains         map[string][]string
		Keys             map[string][]string
	}
	if err := yaml.Unmarshal(phrasingsYAML, &raw); err != nil {
		panic("answer: load phrasings.yaml: " + err.Error())
	}
	norms := func(list []string) (out []string) {
		for _, w := range list {
			out = append(out, " "+norm(w)+" ")
		}
		return out
	}
	v.phrases = map[string]string{}
	for key, list := range raw.Keys {
		for _, p := range list {
			n := norm(p)
			if other, dup := v.phrases[n]; dup {
				panic(fmt.Sprintf("answer: phrasing %q is under %s and %s", p, other, key))
			}
			v.phrases[n] = key
		}
	}
	v.contains = map[string][]string{}
	for key, list := range raw.Contains {
		v.contains[key] = norms(list)
	}
	v.leads, v.consent = norms(raw.Leads), norms(raw.Consent)
	v.ethnicity, v.raceOptions = norms(raw.EthnicityOptions), norms(raw.RaceOptions)
	v.sensitive, v.drafted = map[string]bool{}, map[string]bool{}
	for _, k := range raw.Sensitive {
		v.sensitive[k] = true
	}
	for _, k := range raw.Drafted {
		v.drafted[k] = true
	}
	return v
}()

func norm(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(text.Fold(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func containsAny(padded string, phrases []string) bool {
	return slices.ContainsFunc(phrases, func(p string) bool { return strings.Contains(padded, p) })
}

func keyOf(label, kind string, options []string) string {
	padded := " " + norm(label) + " "
	found := map[string]bool{}
	var sensitive []string
	for key, list := range vocab.contains {
		if containsAny(padded, list) {
			found[key] = true
			if vocab.sensitive[key] {
				sensitive = append(sensitive, key)
			}
		}
	}
	switch {
	case len(sensitive) == 1:
		return sensitive[0]
	case len(sensitive) > 1:
		if len(sensitive) == 2 && found["race"] && found["ethnicity"] {
			joined := " " + norm(strings.Join(options, " ")) + " "
			if containsAny(joined, vocab.ethnicity) && !containsAny(joined, vocab.raceOptions) {
				return "ethnicity"
			}
			return "race"
		}
		return "sensitive"
	}
	n := strings.TrimSpace(padded)
	for {
		if key, ok := vocab.phrases[n]; ok {
			return key
		}
		rest, stripped := "", false
		for _, l := range vocab.leads {
			if r, ok := strings.CutPrefix(" "+n, l); ok {
				rest, stripped = strings.TrimSpace(r), true
				break
			}
		}
		if !stripped {
			break
		}
		n = rest
	}
	if (kind == formread.KindChoice || kind == formread.KindMultipleChoice || kind == formread.KindYesNo) && containsAny(padded, vocab.consent) {
		return "consent"
	}
	switch {
	case found["how_heard"]:
		return "how_heard"
	case found["work_authorization"] && !found["sponsorship"]:
		return "work_authorization"
	case found["sponsorship"] && !found["work_authorization"]:
		return "sponsorship"
	}
	return ""
}

func isChoice(kind string, options []string) bool {
	return len(options) > 0 || kind == formread.KindChoice || kind == formread.KindMultipleChoice
}

func pick(kind string, options []string, vals []Value) ([]string, bool) {
	multi := kind == formread.KindMultipleChoice
	if !multi && len(vals) != 1 {
		return nil, false
	}
	var picked []string
	for _, v := range vals {
		if o, ok := matchOption(options, v); ok {
			picked = append(picked, o)
		}
	}
	return picked, len(picked) > 0
}

func matchOption(options []string, v Value) (string, bool) {
	var wants []string
	for _, w := range append([]string{v.Text}, v.Match...) {
		if n := norm(w); n != "" {
			wants = append(wants, n)
		}
	}
	for _, prefix := range []bool{false, true} {
		for _, o := range options {
			n := norm(o)
			for _, w := range wants {
				if n == w || (prefix && strings.HasPrefix(n, w+" ")) {
					return o, true
				}
			}
		}
	}
	return "", false
}

func joinTexts(vals []Value) string {
	texts := make([]string, len(vals))
	for i, v := range vals {
		texts[i] = v.Text
	}
	return strings.Join(texts, ", ")
}
