package analysis

import (
	"cmp"
	"context"
	_ "embed"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/skills"
)

//go:embed keyfacts.yaml
var keyFactsYAML []byte

type named struct {
	Name     string
	Amount   string
	Patterns []string
}

var keyVocab = func() (v struct {
	YearsNoise     []string `yaml:"years_noise"`
	YearsFollow    []string `yaml:"years_follow"`
	SeniorityNoise []string `yaml:"seniority_noise"`
	SeniorityLeads []string `yaml:"seniority_leads"`
	Levels, Stages []named
	StageNoise     []string `yaml:"stage_noise"`
	AmountNoise    []string `yaml:"amount_noise"`
	ValuationCues  []string `yaml:"valuation_cues"`

	DeadlineCues        []string `yaml:"deadline_cues"`
	Weekdays            []string
	Months              []named
	ContractNouns       []string `yaml:"contract_nouns"`
	ContractLabels      []string `yaml:"contract_labels"`
	LengthQualifiers    []string `yaml:"length_qualifiers"`
	LengthNoise         []string `yaml:"length_noise"`
	FormStarts          []string `yaml:"form_starts"`
	FormAsks            []string `yaml:"form_asks"`
	ClearanceTerms      []named  `yaml:"clearance_terms"`
	ClearanceLevels     []string `yaml:"clearance_levels"`
	ClearanceLeads      []string `yaml:"clearance_leads"`
	CitizenshipCues     []string `yaml:"citizenship_cues"`
	CitizenshipRequired []string `yaml:"citizenship_required"`
	RelocationSupport   []string `yaml:"relocation_support"`
	RelocationLeads     []string `yaml:"relocation_leads"`
	RelocationNoise     []string `yaml:"relocation_noise_after"`
	VisaWords           []string `yaml:"visa_words"`
	VisaSupport         []string `yaml:"visa_support"`
	SponsorObjects      []string `yaml:"sponsor_objects"`
	DenialWords         []string `yaml:"denial_words"`
	ContrastWords       []string `yaml:"contrast_words"`
}) {
	if err := yaml.Unmarshal(keyFactsYAML, &v); err != nil {
		panic("analysis: load keyfacts.yaml: " + err.Error())
	}
	return v
}()

func compileNamed(items []named, wrap func(string) string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, n := range items {
		out = append(out, regexp.MustCompile(`(?i)`+wrap(altRe(n.Patterns))))
	}
	return out
}

func wordsRe(words []string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(?:` + alt(words) + `)\b`)
}

var (
	seniorityNoiseRe, amountNoiseRe, valuationRe = wordsRe(keyVocab.SeniorityNoise), wordsRe(keyVocab.AmountNoise), wordsRe(keyVocab.ValuationCues)
	yearsNoiseRe                                 = regexp.MustCompile(`(?i)\b(?:` + altRe(keyVocab.YearsNoise) + `)\b`)
	yearsFollowRe                                = regexp.MustCompile(`(?i)\b(?:years?|yrs?)['’]?\s+(?:` + alt(keyVocab.YearsFollow) + `)\b`)
	yearWordRe                                   = regexp.MustCompile(`(?i)\b(?:years?|yrs?)\b`)
	amountBeforeRe                               = regexp.MustCompile(`(?:\S+\s+){0,2}$`)
	amountAfterRe                                = regexp.MustCompile(`^\s*\w+(?:\s+\w+)?`)
	stageNoiseRe                                 = regexp.MustCompile(`(?i)\b(?:` + altRe(keyVocab.StageNoise) + `)\b`)
	leads                                        = alt(keyVocab.SeniorityLeads)
	titleRes                                     = compileNamed(keyVocab.Levels, func(w string) string { return `\b(?:` + w + `)\b` })
	textRes                                      = compileNamed(keyVocab.Levels, func(w string) string {
		return `\b(?:(?:` + leads + `)\s+(?:an?\s+)?(?:experienced\s+)?(?:` + w + `)\b|(?:` + w + `)[- ](?:level|role|position|hire)\b)`
	})
	stageRes = compileNamed(keyVocab.Stages, func(w string) string { return `\b(?:` + w + `)` })
	raiseRe  = regexp.MustCompile(`(?i)([$€£])\s?(\d[\d.,]*)\s?(k|mm|mn|m|bn|b|million|billion)?\b`)
	suffixes = map[string]string{"k": "K", "m": "M", "mm": "M", "mn": "M", "million": "M", "b": "B", "bn": "B", "billion": "B"}
)

type keyFacts struct{}

func (keyFacts) Name() string { return "key_facts" }

func (keyFacts) Version() int { return 4 }

func (keyFacts) Produces() []facts.Kind {
	return []facts.Kind{facts.Years, facts.Seniority, facts.CompanyStage, facts.Deadline, facts.ContractLen, facts.Clearance, facts.Relocation, facts.Visa}
}

func (keyFacts) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	var out []facts.Fact
	for _, find := range []func(*Doc) (facts.Fact, bool){yearsAsked, seniorityOf, stageOf, deadlineOf, contractLengthOf, clearanceOf, relocationOf, visaOf} {
		if f, ok := find(doc); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func yearsAsked(doc *Doc) (facts.Fact, bool) {
	hasSection := hasRequirementSection(doc)
	for _, loose := range []bool{false, true} {
		best, most := facts.Fact{}, 0
		for _, line := range doc.lines() {
			section := doc.sectionAt(line.Start)
			eligible := section == SectionRequirements ||
				!hasSection && (section == SectionIntro || section == SectionRole || section == SectionOther) && fallbackRe.MatchString(line.Text)
			if loose {
				eligible = section != SectionCompany && section != SectionNiceToHave && section != SectionOffer && section != SectionProcess && !eeoRe.MatchString(line.Text)
			}
			if !eligible {
				continue
			}
			for _, at := range yearWordRe.FindAllStringIndex(line.Text, -1) {
				sentence := sentenceAround(line.Text, at[0])
				n, ok := skills.YearsIn(sentence)
				if !ok || n <= most || n > 40 || yearsNoiseRe.MatchString(sentence) || loose && !yearsFollowRe.MatchString(sentence) || requirementFact(Line{Text: sentence}, facts.Required).Flag == facts.Preferred {
					continue
				}
				most, best = n, sentenceFact(facts.Years, strconv.Itoa(n), line, at[0])
			}
		}
		if most > 0 {
			return best, true
		}
	}
	return facts.Fact{}, false
}

func levelIn(res []*regexp.Regexp, text string) (int, []int) {
	text = seniorityNoiseRe.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
	for i := len(res) - 1; i >= 0; i-- {
		if loc := res[i].FindStringIndex(text); loc != nil {
			return i, loc
		}
	}
	return -1, nil
}

func seniorityOf(doc *Doc) (facts.Fact, bool) {
	if i, _ := levelIn(titleRes, doc.Title); i >= 0 {
		return vocabFact(facts.Seniority, keyVocab.Levels[i].Name, "", 0, 0), true
	}
	for _, line := range doc.lines() {
		if section := doc.sectionAt(line.Start); section == SectionCompany || section == SectionOffer || section == SectionProcess {
			continue
		}
		if i, loc := levelIn(textRes, line.Text); i >= 0 {
			return vocabFact(facts.Seniority, keyVocab.Levels[i].Name, "", line.Start+loc[0], line.Start+loc[1]), true
		}
	}
	return facts.Fact{}, false
}

func stageOf(doc *Doc) (facts.Fact, bool) {
	best, rank := facts.Fact{}, len(stageRes)
	for _, line := range doc.lines() {
		if !doc.openingOrCompany(line.Start) {
			continue
		}
		for i, re := range stageRes {
			if i >= rank {
				break
			}
			for _, loc := range re.FindAllStringIndex(line.Text, -1) {
				if stageNoiseRe.MatchString(line.Text[sentenceStart(line.Text, loc[0]):loc[0]]) {
					continue
				}
				sentence := sentenceAround(line.Text, loc[0])
				if value, ok := stageValue(keyVocab.Stages[i], sentence); ok {
					best, rank = sentenceFact(facts.CompanyStage, value, line, loc[0]), i
					break
				}
			}
		}
	}
	return best, rank < len(stageRes)
}

func stageValue(stage named, sentence string) (string, bool) {
	raise, valuation := amounts(sentence)
	switch {
	case stage.Amount == "":
		return stage.Name, true
	case raise == "" && stage.Amount == "required":
		return "", false
	case raise != "" && stage.Amount == "required":
		stage.Name += " " + raise
	case raise != "":
		stage.Name += ", " + raise
	}
	if valuation != "" {
		stage.Name += ", valued at " + valuation
	}
	return stage.Name, true
}

func amounts(sentence string) (raise, valuation string) {
	for _, m := range raiseRe.FindAllStringSubmatchIndex(sentence, -1) {
		around := amountBeforeRe.FindString(sentence[:m[0]]) + amountAfterRe.FindString(sentence[m[1]:])
		suffix := ""
		if m[6] >= 0 {
			suffix = suffixes[strings.ToLower(sentence[m[6]:m[7]])]
		}
		amount := sentence[m[2]:m[3]] + strings.TrimRight(sentence[m[4]:m[5]], ".,") + suffix
		switch {
		case valuationRe.MatchString(around):
			valuation = cmp.Or(valuation, amount)
		case !amountNoiseRe.MatchString(around):
			raise = cmp.Or(raise, amount)
		}
	}
	return
}
