package analysis

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

//go:embed conditions.yaml
var conditionsYAML []byte

var plusExtra = `(?:\+|\bplus\b)\s*(?:[\d.,%–-]+\s*(?:of\s+)?)?(?:` + alt(vocab.Money["plus_extras"]) + `)\b`

var (
	languageRe, languageName, proficiencyRe                                                         = loadLanguages()
	remoteRe                                                                                        = regexp.MustCompile(`(?i)\bremote(?:ly)?\b(?:\s+(?:` + alt(vocab.Mode["remote_noise"]) + `))?`)
	remoteNotRe                                                                                     = regexp.MustCompile(`(?i)\b(?:no|not|non|isn't|is not|aren't|are not|never|without)(?:\s+[\w'-]+){0,3}\s*$|\bnon-\s*$`)
	remoteNoiseRe                                                                                   = regexp.MustCompile(`(?i)^(?:remote(?:ly)?\s+(?:` + alt(vocab.Mode["remote_noise"]) + `)|remote[- ]friendly)$`)
	distributedRe                                                                                   = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Mode["distributed"]) + `)\b`)
	hybridRe                                                                                        = regexp.MustCompile(`(?i)\bhybrid\b(?:[- ]+(?:` + alt(vocab.Mode["hybrid_noise"]) + `))?`)
	hybridNoiseRe                                                                                   = regexp.MustCompile(`(?i)^hybrid[- ]+(?:` + alt(vocab.Mode["hybrid_noise"]) + `)$`)
	onsiteRe                                                                                        = regexp.MustCompile(`(?i)\b(?:on-?site|on site|in-person|in-office|in office|in the office|office-based|office based)\b`)
	onsiteNoiseRe                                                                                   = regexp.MustCompile(`(?i)^\s*(?:\w+\s+)?(?:` + altRe(vocab.Mode["onsite_noise"]) + `)\b`)
	daysAfterRe, daysBeforeRe, shareBeforeRe, shareAfterRe, willingRe, cityOrRemoteRe, remoteDaysRe = daysPatterns()
	contractRe, internRe2, employmentRe, smartRe, employmentContractRe                              = engagementPatterns()
	hoursCueRe                                                                                      = regexp.MustCompile(`(?i)\b(?:full[- ]?time|part[- ]?time)\b|` + weeklyPattern)
	zoneCueRe                                                                                       = regexp.MustCompile(`(?i)time ?zones?|±|overlap|working hours|business hours|aligned|hours of|standup|stand-up|meetings`)
	zoneOffsetRe                                                                                    = regexp.MustCompile(`(?i)\b(?:UTC|GMT)\s*[+\-]`)
	placeCueRes, eligibilityRe, citizenRe, worldwideRe                                              = placePatterns()
	basedOnlyRe                                                                                     = regexp.MustCompile(`\b(\p{Lu}[\p{L}'-]+(?: \p{Lu}[\p{L}'-]+)?),? based(?: only)?\b`)
	placeLeadRe                                                                                     = regexp.MustCompile(`(?i)^(?:anywhere\s+(?:in|within)\s+)?(?:(?:the|our|an?|their|its)\s+)?`)
	placeTailRe                                                                                     = regexp.MustCompile(`(?i)(?:,?\s*[^,]*\bpreferred\b.*|\s+(?:offices?|area|hub|hq|headquarters|only|based)|-based)$`)
	zoneWordRe                                                                                      = regexp.MustCompile(`(?i)time ?zones?`)
	languageNoiseRe                                                                                 = regexp.MustCompile(`^\s+law\b`)
	submitInRe                                                                                      = regexp.MustCompile(`(?i)\b(?:submit|send|apply|application|resume|cv|cover letter)\b[^.]{0,60}\bin\s*$`)
	placeStopRe                                                                                     = regexp.MustCompile(`(?i)\s+(?:with|who|where|for|to|as|you|we|our|that|but|if|so|is|are|was|will|has|have|can|at|on)\b.*$`)
	moneyCueRe                                                                                      = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Money["cue_words"]) + `|(?:per|a|an) (?:` + alt(vocab.Money["cue_periods"]) + `))\b|/\s?(?:` + alt(vocab.Money["cue_units"]) + `)\b`)
	fundingRe                                                                                       = regexp.MustCompile(`(?i)\b(?:` + altRe(vocab.Money["funding"]) + `)\b`)
	moneyRe                                                                                         = moneyPattern()
	onsiteBeforeNoiseRe                                                                             = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Mode["before_noise"]) + `)\s+(?:\w+\s+){0,2}$`)
	notPayRe                                                                                        = regexp.MustCompile(`(?i)\b(?:` + alt(append(slices.Clone(vocab.Money["not_pay_both"]), vocab.Money["not_pay_clause"]...)) + `)\b`)
	notCashRe                                                                                       = regexp.MustCompile(`(?i)\b(?:` + alt(append(slices.Clone(vocab.Money["not_pay_both"]), vocab.Money["not_pay_sentence"]...)) + `)\b`)
	clauseSepRe                                                                                     = regexp.MustCompile(`(?i)\.\s|[;|•()]|\s\+\s|\b(?:plus|and|with|including|includes)\b`)
	plusFollowsRe                                                                                   = regexp.MustCompile(`(?i)^\s*` + plusExtra)
	typoRangeRe                                                                                     = regexp.MustCompile(`^(?:[^\d\s]\s?)?\d{1,3}(?:,\d{3})+\s?(?:-|–|—|to)\s?(?:[^\d\s]\s?)?\d{1,3},\d{2}\b`)
	typeContractRe                                                                                  = regexp.MustCompile(`(?i)(?:^|[|,;(\s-])(?:(?:` + alt(vocab.Engagement["type_labels"]) + `)\s*:?|(?:` + alt(vocab.Engagement["type_labels_with_colon"]) + `)\s*:)\s*[|–-]?\s*(?:` + alt(vocab.Engagement["type_values"]) + `)\b`)
	typeFollowRe                                                                                    = regexp.MustCompile(`(?i)^\s*(?:$|[,.;:|)(–-]|(?:` + alt(vocab.Engagement["type_follow"]) + `)\b)`)
	contractNoiseRe                                                                                 = regexp.MustCompile(`(?i)^\s+(?:` + alt(vocab.Engagement["contract_noise_after"]) + `|` + alt(vocab.Engagement["authorization_words"]) + `)\b`)
	authorizationRe                                                                                 = regexp.MustCompile(`(?i)^\s+(?:` + alt(vocab.Engagement["authorization_words"]) + `)\b`)
	typeHeaderRe                                                                                    = regexp.MustCompile(`(?i)^(?:` + alt(vocab.Engagement["type_headers"]) + `)$`)
	typeCellRe                                                                                      = regexp.MustCompile(`(?i)^(?:` + alt(vocab.Engagement["type_cells"]) + `)$`)
	eeoRe                                                                                           = regexp.MustCompile(`(?i)(?:` + alt(vocab.Engagement["eeo_cues"]) + `)`)
	partyRe                                                                                         = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Place["other_parties"]) + `)\b[^.;!?]{0,50}$`)
	perkRe                                                                                          = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Mode["perk_words"]) + `)\b[^.;]{0,40}$`)
	shareRemoteRe                                                                                   = regexp.MustCompile(`(?i)(\d{1,3})\s?%\s+(?:of\s+(?:the\s+)?time\s+)?(?:telecommut\w*|remote\w*|work(?:ing)?\s+from\s+home|wfh)`)
	officeBasedRe                                                                                   = regexp.MustCompile(`(?i)\b(?:role|position|job|team)\s+is\s+(?:based|located)\s+(?:in|at)\s+(?:our|the)\s+[^.;]{0,40}?\b(?:hq|headquarters|offices?|campus)\b`)
	hybridNounRe                                                                                    = regexp.MustCompile(`(?i)^\s+(?:` + alt(vocab.Mode["hybrid_nouns"]) + `)\b`)
	workplaceRe                                                                                     = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Mode["workplace_words"]) + `)\b`)
	softZoneRe                                                                                      = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Place["soft_zone"]) + `)\b`)
	companyContractorRe                                                                             = regexp.MustCompile(`(?i)\b(?:(?:` + alt(vocab.Engagement["employer_adjectives"]) + `)\s+contractors?|` + altRe(vocab.Engagement["employer_phrases"]) + `|(?:` + alt(vocab.Engagement["employer_leads"]) + `)\s+(?:an?\s+)?(?:[\w-]+\s+){0,3}contractors?)\b`)
	negationRe                                                                                      = regexp.MustCompile(`(?i)(?:\b(?:not|never|no|without|neither|nor|except|unless|exclud\w+)\b|n['’]t\b)[^.;!?]{0,40}$`)
	parenBasedRe                                                                                    = regexp.MustCompile(`\(([^()\n]{2,30})\)[ -]?based\b`)
	plusExtraRe                                                                                     = regexp.MustCompile(`(?i)` + plusExtra + `.*`)
	moneyScaleRe                                                                                    = regexp.MustCompile(`(?i)^\s?(?:m|mm|b|bn|million|billion)\b`)
)

func loadLanguages() (*regexp.Regexp, map[string]string, *regexp.Regexp) {
	var v struct {
		Languages   map[string][]string
		Proficiency []string
	}
	if err := yaml.Unmarshal(conditionsYAML, &v); err != nil {
		panic("analysis: load conditions.yaml: " + err.Error())
	}
	names := map[string]string{}
	var words, prof []string
	for name, aliases := range v.Languages {
		for _, w := range append([]string{name}, aliases...) {
			names[w] = name
			words = append(words, regexp.QuoteMeta(w))
		}
	}
	sort.Strings(words)
	for _, p := range v.Proficiency {
		prof = append(prof, regexp.QuoteMeta(p))
	}
	return regexp.MustCompile(`\b(` + strings.Join(words, "|") + `)\b`), names, regexp.MustCompile(`(?i)\b(?:` + strings.Join(prof, "|") + `)\b`)
}

func moneyPattern() *regexp.Regexp {
	cur := `(?:[` + regexp.QuoteMeta(symbols) + `]|` + alt(currencyMarkers) + `|\b(?:` + strings.Join(currencyCodes, "|") + `)\b)`
	num := `\d{1,3}(?:[,\s\x{00a0}\x{202f}]\d{3})+|\d+(?:\.\d+)?`
	one := `(?:` + cur + `\s?)?(?:` + num + `)\s?[kK]?(?:\s?` + cur + `)?`
	return regexp.MustCompile(`(?i)` + one + `\s?(?:-|–|—|to)\s?` + one + `(?:\s?` + cur + `)?|` + cur + `\s?(?:` + num + `)\s?[kK]?|(?:` + num + `)\s?[kK]?\s?` + cur)
}

const worldwideLabel = "Worldwide"

type conditions struct{}

func (conditions) Name() string { return "conditions" }

func (conditions) Version() int { return 14 }

func (conditions) Produces() []facts.Kind {
	return []facts.Kind{facts.Money, facts.WorkMode, facts.Engagement, facts.Place, facts.TimeZone, facts.Hours, facts.Language}
}

func vocabFact(kind facts.Kind, value string, flag facts.Flag, start, end int) facts.Fact {
	return facts.Fact{Kind: kind, Value: value, Flag: flag, Start: start, End: end, Source: facts.Source{Way: facts.Vocabulary}}
}

type modeCue struct {
	mode       string
	start, end int
}

func (conditions) Extract(_ context.Context, doc *Doc) ([]facts.Fact, error) {
	var out []facts.Fact
	skip := map[facts.Kind]bool{}
	var strong, weak []modeCue
	languages := map[string]facts.Fact{}
	engagements := map[string]bool{}
	typeRows := 99
	for _, line := range doc.lines() {
		section := doc.sectionAt(line.Start)
		if !skip[facts.Money] {
			if f, ok := moneyIn(line); ok {
				out, skip[facts.Money] = append(out, f), true
			}
		}
		if section != SectionProcess && !skip[facts.WorkMode] {
			for _, c := range modeCues(line) {
				if section == SectionOffer || section == SectionCompany {
					weak = append(weak, c)
				} else {
					strong = append(strong, c)
				}
			}
		}
		if section != SectionProcess && section != SectionRequirements && section != SectionNiceToHave && !eeoRe.MatchString(line.Text) {
			for _, e := range []struct {
				value string
				re    *regexp.Regexp
				ok    func(after string) bool
			}{
				{"contract", contractRe, func(after string) bool { return !contractNoiseRe.MatchString(after) }},
				{"contract", typeContractRe, typeFollowRe.MatchString},
				{"internship", internRe2, nil},
				{"employment", employmentRe, func(after string) bool { return !authorizationRe.MatchString(after) }},
			} {
				if loc := statementIn(e.re, line.Text, e.ok); loc != nil && !engagements[e.value] {
					engagements[e.value] = true
					out = append(out, vocabFact(facts.Engagement, e.value, "", line.Start+loc[0], line.Start+loc[1]))
				}
			}
			if !engagements["contract"] {
				if typeRows = typeRows + 1; typeCells(line.Text, typeHeaderRe) {
					typeRows = 0
				} else if typeRows <= 8 && typeCells(line.Text, typeCellRe) {
					engagements["contract"] = true
					out = append(out, vocabFact(facts.Engagement, "contract", "", line.Start, line.End))
				}
			}
		}
		if !skip[facts.Hours] {
			if loc := hoursCueRe.FindStringIndex(line.Text); loc != nil {
				if h := ParseHours(line.Text); h != nil {
					b, _ := json.Marshal(h)
					out, skip[facts.Hours] = append(out, vocabFact(facts.Hours, string(b), "", line.Start+loc[0], line.Start+loc[1])), true
				}
			}
		}
		if !skip[facts.TimeZone] {
			if s, e, ok := places.ZoneMention(line.Text); ok && (zoneCueRe.MatchString(line.Text) || zoneOffsetRe.MatchString(line.Text)) {
				out, skip[facts.TimeZone] = append(out, vocabFact(facts.TimeZone, line.Text[s:e], "", line.Start+s, line.Start+e)), true
			}
		}
		out = append(out, linePlaces(line, section)...)
		for _, f := range languagesIn(line, section) {
			if old, seen := languages[f.Value]; !seen || old.Flag == facts.Preferred && f.Flag == facts.Required {
				languages[f.Value] = f
			}
		}
	}
	seenPlace := map[string]bool{}
	unique := out[:0]
	for _, f := range out {
		if key := strings.ToLower(f.Value); f.Kind != facts.Place || !seenPlace[key] {
			seenPlace[key] = f.Kind == facts.Place || seenPlace[key]
			unique = append(unique, f)
		}
	}
	out = unique
	if textRestrictsPlace(doc) {
		out = withoutWorldwide(out)
	}
	if inTitle := cuePlaces(Line{Text: doc.Title}, placeCueRes...); len(inTitle) > 0 {
		kept := out[:0]
		for _, f := range out {
			if f.Kind != facts.Place {
				kept = append(kept, f)
			}
		}
		out = kept
		for _, f := range inTitle {
			out = append(out, vocabFact(facts.Place, f.Value, "", 0, 0))
		}
	}
	cues := strong
	if len(cues) == 0 {
		cues = weak
	}
	seen := map[string]bool{}
	for _, c := range cues {
		if !seen[c.mode] {
			seen[c.mode] = true
			out = append(out, vocabFact(facts.WorkMode, c.mode, "", c.start, c.end))
		}
	}
	names := make([]string, 0, len(languages))
	for n := range languages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, languages[n])
	}
	return out, nil
}

func moneyIn(line Line) (facts.Fact, bool) {
	text := line.Text
	for _, loc := range moneyRe.FindAllStringIndex(text, -1) {
		for loc[1] > loc[0] && text[loc[1]-1] == ' ' {
			loc[1]--
		}
		seg := text[loc[0]:loc[1]]
		if typo := typoRangeRe.FindString(text[loc[0]:]); typo != "" {
			seg, loc[1] = typo+"0", loc[0]+len(typo)
		}
		sentence := sentenceAround(text, loc[0])
		if moneyScaleRe.MatchString(text[loc[1]:]) || fundingRe.MatchString(sentence) || notPayRe.MatchString(clauseAround(text, loc[0], loc[1])) {
			continue
		}
		m := ParseMoney(seg, sentence)
		if m == nil || m.Currency == "" || m.Min <= 0 && m.Max <= 0 {
			continue
		}
		cue := moneyCueRe.MatchString(sentence) || plusFollowsRe.MatchString(text[loc[1]:])
		if !cue && !(m.Min > 0 && m.Max > m.Min && m.Max >= 1000) {
			continue
		}
		b, _ := json.Marshal(m)
		return vocabFact(facts.Money, string(b), "", line.Start+loc[0], line.Start+loc[1]), true
	}
	return facts.Fact{}, false
}

func clauseAround(text string, from, to int) string {
	start, end := 0, len(text)
	for _, sep := range clauseSepRe.FindAllStringIndex(text, -1) {
		if sep[1] <= from {
			start = sep[1]
		} else if sep[0] >= to {
			end = min(end, sep[0])
		}
	}
	return text[start:end]
}

var abbreviationRe = regexp.MustCompile(`(?:^|[\s(])(?:\p{Lu}\.)*\p{Lu}$|\b(?:Inc|Ltd|Corp|Co|Mr|Mrs|Ms|Dr|St|vs|etc)$`)

func sentenceStart(text string, at int) int {
	for i := strings.LastIndex(text[:at], ". "); i >= 0; i = strings.LastIndex(text[:i], ". ") {
		if !abbreviationRe.MatchString(text[:i]) {
			return i + 1
		}
	}
	return 0
}

func sentenceAround(text string, at int) string {
	start := max(sentenceStart(text, at), strings.LastIndex(text[:at], "|")+1)
	end := len(text)
	for i := strings.Index(text[at:], ". "); i >= 0; {
		if !abbreviationRe.MatchString(text[:at+i]) {
			end = min(end, at+i+1)
			break
		}
		next := strings.Index(text[at+i+2:], ". ")
		if next < 0 {
			break
		}
		i += 2 + next
	}
	if i := strings.Index(text[at:], "|"); i >= 0 {
		end = min(end, at+i+1)
	}
	return plusExtraRe.ReplaceAllString(strings.TrimSpace(text[start:end]), "")
}

var dayCounts = map[string]string{"1": "hybrid", "2": "hybrid", "3": "hybrid", "4": "hybrid", "5": "onsite", "one": "hybrid", "two": "hybrid", "three": "hybrid", "four": "hybrid", "five": "onsite"}

func modeCues(line Line) []modeCue {
	text := line.Text
	var cues []modeCue
	add := func(mode string, from, to int) {
		if !remoteNotRe.MatchString(text[:from]) {
			cues = append(cues, modeCue{mode, line.Start + from, line.Start + to})
		}
	}
	partial := false
	if m := shareRemoteRe.FindStringSubmatchIndex(text); m != nil {
		partial = true
		mode := "remote"
		if n, _ := strconv.Atoi(text[m[2]:m[3]]); n < 100 {
			mode = "hybrid"
		}
		add(mode, m[0], m[1])
	} else if m := daysAfterRe.FindStringSubmatchIndex(text); m != nil {
		partial = true
		add(dayCounts[strings.ToLower(text[m[2]:m[3]])], m[0], m[1])
	} else if m := daysBeforeRe.FindStringSubmatchIndex(text); m != nil && !remoteDaysRe.MatchString(text[m[2]:m[3]]) {
		partial = true
		add(dayCounts[strings.ToLower(text[m[4]:m[5]])], m[0], m[1])
	} else if m := shareBeforeRe.FindStringSubmatchIndex(text); m != nil {
		partial = true
		add(shareMode(text[m[2]:m[3]]), m[0], m[1])
	} else if m := shareAfterRe.FindStringSubmatchIndex(text); m != nil {
		partial = true
		add(shareMode(text[m[2]:m[3]]), m[0], m[1])
	}
	hybrid := len(workHybrids(text)) > 0
	if !partial {
		for _, loc := range remoteRe.FindAllStringIndex(text, -1) {
			if hybrid && strings.EqualFold(text[loc[0]:loc[1]], "remotely") {
				continue
			}
			after := strings.ToLower(text[loc[1]:])
			if !remoteNoiseRe.MatchString(text[loc[0]:loc[1]]) && !remoteNotRe.MatchString(text[:loc[0]]) && !strings.HasPrefix(after, "-friendly") && !strings.HasPrefix(after, " friendly") {
				add("remote", loc[0], loc[1])
				break
			}
		}
		if loc := distributedRe.FindStringIndex(text); loc != nil {
			add("remote", loc[0], loc[1])
		}
	}
	if hs := workHybrids(text); len(hs) > 0 {
		add("hybrid", hs[0][0], hs[0][1])
	}
	if !partial {
		for _, loc := range onsiteRe.FindAllStringIndex(text, -1) {
			if !onsiteNoiseRe.MatchString(text[loc[1]:]) && !onsiteBeforeNoiseRe.MatchString(text[:loc[0]]) && !perkRe.MatchString(text[:loc[0]]) && !remoteNotRe.MatchString(text[:loc[0]]) {
				add("onsite", loc[0], loc[1])
				break
			}
		}
		if m := willingRe.FindStringSubmatchIndex(text); m != nil && len(placeItems(text[m[2]:m[3]])) > 0 {
			add("onsite", m[0], m[1])
		}
		if loc := officeBasedRe.FindStringIndex(text); loc != nil {
			add("onsite", loc[0], loc[1])
		}
		if len(text) <= 100 && hasCityOrRemote(text) {
			add("onsite", 0, len(text))
		}
	}
	if strings.Count(text, "|") >= 2 {
		at := 0
		for _, seg := range strings.Split(text, "|") {
			if mode, ok := ParseMode(seg); ok {
				add(mode, at+strings.Index(seg, strings.TrimSpace(seg)), at+len(strings.TrimRight(seg, " ")))
			} else if hasCityOrRemote(strings.TrimSpace(seg)) {
				add("onsite", at+strings.Index(seg, strings.TrimSpace(seg)), at+len(strings.TrimRight(seg, " ")))
			}
			at += len(seg) + 1
		}
	}
	return cues
}

func workHybrids(text string) [][]int {
	var out [][]int
	for _, loc := range hybridRe.FindAllStringIndex(text, -1) {
		if hybridNoiseRe.MatchString(text[loc[0]:loc[1]]) || remoteNotRe.MatchString(text[:loc[0]]) {
			continue
		}
		if hybridNounRe.MatchString(text[loc[1]:]) && !workplaceRe.MatchString(sentenceAround(text, loc[0])) {
			continue
		}
		out = append(out, loc)
	}
	return out
}

func shareMode(percent string) string {
	if n, _ := strconv.Atoi(percent); n >= 100 {
		return "onsite"
	}
	return "hybrid"
}

func hasCityOrRemote(text string) bool {
	m := cityOrRemoteRe.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	for _, it := range placeItems(m[1] + " " + m[2]) {
		if places.IsCity(it.label) {
			return true
		}
	}
	return false
}

func cleanPlace(label string) (string, int) {
	label = strings.TrimSpace(placeStopRe.ReplaceAllString(label, ""))
	lead := 0
	if loc := placeLeadRe.FindStringIndex(label); loc != nil {
		lead, label = loc[1], label[loc[1]:]
	}
	label = placeTailRe.ReplaceAllString(label, "")
	label = strings.TrimRight(label, " ,")
	if trimmed := strings.TrimRight(label, "."); !strings.Contains(trimmed, ".") {
		label = trimmed
	}
	return label, lead
}

func placeFacts(line Line, text string, from int) []facts.Fact {
	var out []facts.Fact
	for _, it := range segmentItems(text) {
		if keepPlace(it.label) {
			out = append(out, vocabFact(facts.Place, it.label, "", line.Start+from+it.start, line.Start+from+it.end))
		}
	}
	return out
}

func keepPlace(label string) bool {
	if notPlace(label) {
		return false
	}
	if len([]rune(label)) <= 2 && !slices.ContainsFunc(vocab.Place["short_codes"], func(c string) bool { return strings.EqualFold(c, label) }) {
		return false
	}
	p := places.Resolve(label)
	return !p.Worldwide || len(p.Countries) > 0 || len(p.Cities) > 0
}

func withoutWorldwide(in []facts.Fact) []facts.Fact {
	out := in[:0]
	for _, f := range in {
		if f.Kind != facts.Place || f.Value != worldwideLabel {
			out = append(out, f)
		}
	}
	return out
}

func linePlaces(line Line, section string) []facts.Fact {
	if section == SectionProcess {
		return cuePlaces(line, eligibilityRe)
	}
	return placesIn(line)
}

func textRestrictsPlace(doc *Doc) bool {
	for _, line := range doc.lines() {
		if !softZoneRe.MatchString(line.Text) {
			if zoneWordRe.MatchString(line.Text) {
				return true
			}
			if _, _, ok := places.ZoneMention(line.Text); ok && (zoneCueRe.MatchString(line.Text) || zoneOffsetRe.MatchString(line.Text)) {
				return true
			}
		}
		for _, f := range linePlaces(line, doc.sectionAt(line.Start)) {
			if f.Value != worldwideLabel {
				return true
			}
		}
	}
	return false
}

func statementIn(re *regexp.Regexp, text string, ok func(after string) bool) []int {
	text = blank(companyContractorRe, text)
	for _, loc := range re.FindAllStringIndex(text, -1) {
		if !smartRe.MatchString(text[:loc[0]]) && !negationRe.MatchString(text[:loc[0]]) && (ok == nil || ok(text[loc[1]:])) {
			return loc
		}
	}
	return nil
}

func typeCells(text string, re *regexp.Regexp) bool {
	for _, cell := range strings.Split(text, "|") {
		if re.MatchString(strings.TrimSpace(cell)) {
			return true
		}
	}
	return false
}

func placesIn(line Line) []facts.Fact {
	out := cuePlaces(line, placeCueRes...)
	out = append(out, cuePlaces(line, citizenRe, parenBasedRe)...)
	for _, m := range officeRe.FindAllStringSubmatchIndex(line.Text, -1) {
		out = append(out, placeFacts(line, line.Text[m[2]:m[3]], m[2])...)
	}
	for _, m := range basedOnlyRe.FindAllStringSubmatchIndex(line.Text, -1) {
		out = append(out, placeFacts(line, line.Text[m[2]:m[3]], m[2])...)
	}
	if strings.Count(line.Text, "|") >= 2 {
		at := 0
		for _, seg := range strings.Split(line.Text, "|") {
			out = append(out, placeFacts(line, seg, at)...)
			at += len(seg) + 1
		}
	}
	if loc := worldwideRe.FindStringIndex(line.Text); loc != nil {
		out = append(out, vocabFact(facts.Place, worldwideLabel, "", line.Start+loc[0], line.Start+loc[1]))
	}
	return out
}

func cuePlaces(line Line, res ...*regexp.Regexp) []facts.Fact {
	var out []facts.Fact
	for _, re := range res {
		for _, m := range re.FindAllStringSubmatchIndex(line.Text, -1) {
			if !partyRe.MatchString(line.Text[sentenceStart(line.Text, m[0]):m[0]]) {
				out = append(out, placeFacts(line, line.Text[m[2]:m[3]], m[2])...)
			}
		}
	}
	return out
}

func onlyLanguages(text string) bool {
	rest := languageRe.ReplaceAllString(text, " ")
	rest = strings.NewReplacer(",", " ", "/", " ", "&", " ", "(", " ", ")", " ").Replace(rest)
	for _, w := range strings.Fields(strings.ToLower(rest)) {
		switch w {
		case "and", "or", "language", "languages", "level":
		default:
			return false
		}
	}
	return true
}

func languagesIn(line Line, section string) []facts.Fact {
	var out []facts.Fact
	for _, loc := range languageRe.FindAllStringIndex(line.Text, -1) {
		submit := submitInRe.MatchString(line.Text[:loc[0]])
		skipped := section == SectionCompany || section == SectionOffer
		if skipped && !submit || languageNoiseRe.MatchString(line.Text[loc[1]:]) {
			continue
		}
		listed := (section == SectionRequirements || section == SectionNiceToHave) && onlyLanguages(line.Text)
		if !submit && !listed && !proficiencyRe.MatchString(line.Text) {
			continue
		}
		flag := facts.Required
		if section == SectionNiceToHave || preferredRe.MatchString(line.Text) {
			flag = facts.Preferred
		}
		out = append(out, vocabFact(facts.Language, languageName[line.Text[loc[0]:loc[1]]], flag, line.Start+loc[0], line.Start+loc[1]))
	}
	return out
}

type vocabulary struct {
	Engagement, Place, Mode, Money map[string][]string
	Currencies                     map[string][]string
	Floors                         map[string]float64
	FloorWithoutPeriod             float64 `yaml:"floor_without_period"`
}

var vocab = loadVocabulary()

func loadVocabulary() vocabulary {
	var v vocabulary
	if err := yaml.Unmarshal(conditionsYAML, &v); err != nil {
		panic("analysis: load conditions.yaml: " + err.Error())
	}
	return v
}

func alt(words []string) string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, strings.ReplaceAll(regexp.QuoteMeta(w), " ", `\s+`))
	}
	return strings.Join(out, "|")
}

func altRe(patterns []string) string { return strings.Join(patterns, "|") }

func engagementPatterns() (contract, intern, employment, smart, employmentContract *regexp.Regexp) {
	e := vocab.Engagement
	internWord := internWordPattern
	contract = regexp.MustCompile(`(?i)\b(?:(?:` + alt(e["contract_heads"]) + `)[\s-]+(?:` + alt(e["contract_nouns"]) + `)|` + alt(e["contract_words"]) +
		`|as\s+(?:an?\s+)?(?:independent\s+|external\s+)?contractor|b2b\s+(?:` + alt(e["b2b_nouns"]) + `)|b2b\s*(?:or|and|/)\s*(?:employment|permanent|uop|employee|contract)` +
		`|` + lengthSpan + `[- ]\s*(?:months?|years?)\s+(?:fixed[- ]term\s+)?contract|(?:short|long)[- ]term\s+contract|contract\s*,\s*` + lengthSpan + `\s*(?:months?|weeks?|years?))\b`)
	intern = regexp.MustCompile(`(?i)\b(?:(?:` + internWord + `)\s+(?:` + alt(e["intern_nouns"]) + `)|(?:` + alt(e["intern_leads"]) + `)\s+(?:` + internWord + `)|(?:` + alt(e["intern_hire_cues"]) + `)\s+(?:[\w-]+\s+){0,3}interns?|(?:as|for)\s+an?\s+(?:[\w-]+\s+)?intern)\b|(?:^|[.!?]\s+)(?:[\w-]+\s+){0,3}interns?\s+(?:` + alt(e["intern_verbs"]) + `)\b`)
	employment = regexp.MustCompile(`(?i)\bpermanent\s+(?:` + alt(e["permanent_nouns"]) + `)\b|(?:^|[,;|•·:]\s*)permanent\s*(?:$|[,;|•·.])|\b(?:full|part)[- ]?time\s+permanent\b|\bw-?2\b|\b(?:full|part)[- ]?time\s+employ(?:ees?|ment)\b|\bemployment\s+contract\b|\bcontract\s+of\s+employment\b|\bemployee\s+(?:position|role|basis|status)\b|\bas\s+an?\s+employee\b`)
	smart = regexp.MustCompile(`(?i)\bsmart\s+$`)
	employmentContract = regexp.MustCompile(`employment contract|contract of employment`)
	return
}

func daysPatterns() (after, before, shareBefore, shareAfter, willing, cityOrRemote, remoteDays *regexp.Regexp) {
	n := `(\d|one|two|three|four|five)`
	office := `(?:office|in[- ]person|on-?site)`
	perWeek := `\s*(?:a|per|each|every|/)\s*week`
	after = regexp.MustCompile(`(?i)\b` + n + `\s+days?(?:` + perWeek + `)?\s+(?:(?:in|at|from)\s+(?:the\s+|our\s+|an?\s+)?)?` + office + `\b`)
	before = regexp.MustCompile(`(?i)\b` + office + `\b([^.;]{0,40}?)\b` + n + `\s+days?` + perWeek + `\b`)
	shareBefore = regexp.MustCompile(`(?i)(\d{1,3})\s?%\s+(?:of\s+(?:the\s+|your\s+|their\s+|each\s+)?(?:time|week|working time|working hours|workweek|work week))?[^.;]{0,25}?\b` + office + `\b`)
	shareAfter = regexp.MustCompile(`(?i)\b` + office + `\b[^.;]{0,40}?(\d{1,3})\s?%`)
	willing = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Place["willing_cues"]) + `)\s+((?:the\s+|our\s+)?[\p{L}][\p{L} .,'-]{1,30})`)
	cityOrRemote = regexp.MustCompile(`(?i)^\W*(?:locations?\s*:\s*)?(.{2,40}?)\s*(?:\bor\b|/|\|)\s*remote\b|\bremote\b\s*(?:\bor\b|/|\|)\s*([^(,;.]{2,40})`)
	remoteDays = regexp.MustCompile(`(?i)\b(?:remote|home|wfh)\b`)
	return
}

func placePatterns() (cues []*regexp.Regexp, eligibility, citizen, worldwide *regexp.Regexp) {
	capture := `((?:(?-i:[A-Z])\.|[^.;:()\n?]){2,60})`
	cues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bremote\s*\(([^)\n]{2,40})\)`),
		regexp.MustCompile(`(?i)\bremote\s*(?:[-–—|:]|,)\s*([^.;:()\n?]{2,40})`),
	}
	for _, cue := range vocab.Place["cues"] {
		cues = append(cues, regexp.MustCompile(`(?i)\b`+alt([]string{cue})+`\s+`+capture))
	}
	eligibility = regexp.MustCompile(`(?i)\b(?:` + alt(vocab.Place["eligibility_cues"]) + `)\s+` + capture)
	citizen = regexp.MustCompile(`(?i)\b(?:must|need to|have to|required to)\s+be\s+(?:an?\s+)?([\p{L}.]+(?:\s[\p{L}]+)?)\s+(?:` + alt(vocab.Place["citizen_words"]) + `)\b`)
	context := alt(vocab.Place["worldwide_context"])
	phrases := alt(vocab.Place["worldwide_phrases"])
	worldwide = regexp.MustCompile(`(?i)\b(?:(?:` + context + `)\b[^.;]{0,12}?\b(?:` + phrases + `)|(?:` + alt(vocab.Place["worldwide_statements"]) + `))\b`)
	return
}
