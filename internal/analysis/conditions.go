package analysis

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

//go:embed conditions.yaml
var conditionsYAML []byte

var (
	languageRe, languageName, proficiencyRe = loadLanguages()
	remoteRe                                = regexp.MustCompile(`(?i)\bremote(?:ly)?\b(?:\s+(?:access|control|server|servers|sensing|procedure|monitoring|debugging|repository|repositories|database|machine|host|desktop|device|devices|api|call|calls|code|execution))?`)
	remoteNotRe                             = regexp.MustCompile(`(?i)\b(?:no|not|non|isn't|is not|aren't|are not|never|without)(?:\s+[\w'-]+){0,3}\s*$|\bnon-\s*$`)
	remoteNoiseRe                           = regexp.MustCompile(`(?i)^(?:remote(?:ly)?\s+(?:access|control|server|servers|sensing|procedure|monitoring|debugging|repository|repositories|database|machine|host|desktop|device|devices|api|call|calls|code|execution)|remote[- ]friendly)$`)
	distributedRe                           = regexp.MustCompile(`(?i)\b(?:distributed (?:team|teams|company|workforce|organi[sz]ation)|work(?:ing)? from (?:anywhere|home)|remote-based)\b`)
	hybridRe                                = regexp.MustCompile(`(?i)\bhybrid\b(?:[- ]+(?:cloud|app|apps|application|mobile|cars?|vehicles?|search|retrieval|quantum|models?\s+of\s+ai))?`)
	hybridNoiseRe                           = regexp.MustCompile(`(?i)^hybrid[- ]+(?:cloud|app|apps|application|mobile|cars?|vehicles?|search|retrieval|quantum|models?\s+of\s+ai)$`)
	onsiteRe                                = regexp.MustCompile(`(?i)\b(?:on-?site|on site|in-office|in office|in the office|office-based|office based)\b`)
	onsiteNoiseRe                           = regexp.MustCompile(`(?i)^\s*(?:\w+\s+)?(?:interviews?|visits?|doctors?|services?|gyms?|lunch\w*|cafeteria|canteen|childcare|daycare|day care|parking|fitness|restaurants?|meals?|caf[eé]s?|kitchen|massages?|health|clinic|nurse|amenities|facilities|training|food|coffee|snacks|barista|laundry|dentist|pool)\b`)
	daysRe                                  = regexp.MustCompile(`(?i)\b(\d|one|two|three|four|five)\s+days?\s+(?:a|per|each|every)\s+week\b[^.;]{0,40}\b(?:office|in[- ]person|on-?site)\b|\b(?:office|in[- ]person|on-?site)\b[^.;]{0,40}?\b(\d|one|two|three|four|five)\s+days?\s+(?:a|per|each|every)\s+week\b`)
	contractRe                              = regexp.MustCompile(`(?i)\b(?:contractors?|freelanc\w*|b2b|1099|fixed[- ]term|temporary|interim|fractional|internship|(?:\d+[- ]month |short[- ]term |long[- ]term )?contract (?:position|role|basis|opportunity|job|engagement|work|to hire|-to-hire)|(?:type|status)\s*:\s*contract)\b`)
	employmentRe                            = regexp.MustCompile(`(?i)\b(?:full[- ]?time|part[- ]?time|permanent|w-?2)\b`)
	hoursCueRe                              = regexp.MustCompile(`(?i)\b(?:full[- ]?time|part[- ]?time)\b|\d+(?:\.\d+)?(?:\s*(?:-|–|—|to)\s*\d+(?:\.\d+)?)?\s*(?:h|hr|hrs|hours)\s*(?:/|per)?\s*week`)
	zoneCueRe                               = regexp.MustCompile(`(?i)time ?zones?|±|overlap|working hours|business hours|aligned|hours of|standup|stand-up|meetings`)
	zoneOffsetRe                            = regexp.MustCompile(`(?i)\b(?:UTC|GMT)\s*[+\-]`)
	placeCueRe                              = regexp.MustCompile(`(?i)\b(?:based in|located in|office in|offices in|hub in|hiring in|hiring from|work from|reside in|residing in|live in|living in|eligible to work in|authori[sz]ed to work in|open to candidates in|remote(?:ly)? (?:in|within|from|across|only in))\s+((?:(?-i:[A-Z])\.|[^.;:()\n]){2,60})|\bremote\s*\(([^)\n]{2,40})\)`)
	basedOnlyRe                             = regexp.MustCompile(`\b(\p{Lu}[\p{L}'-]+(?: \p{Lu}[\p{L}'-]+)?),? based(?: only)?\b`)
	placeLeadRe                             = regexp.MustCompile(`(?i)^(?:the|our|an?|their|its)\s+`)
	placeTailRe                             = regexp.MustCompile(`(?i)(?:,?\s*[^,]*\bpreferred\b.*|\s+(?:offices?|area|hub|hq|headquarters))$`)
	zoneWordRe                              = regexp.MustCompile(`(?i)time ?zones?`)
	languageNoiseRe                         = regexp.MustCompile(`^\s+law\b`)
	submitInRe                              = regexp.MustCompile(`(?i)\b(?:submit|send|apply|application|resume|cv|cover letter)\b[^.]{0,60}\bin\s*$`)
	placeStopRe                             = regexp.MustCompile(`(?i)\s+(?:with|who|where|for|to|as|you|we|our|that|but|if|so|is|are|was|will|has|have|can|at|on)\b.*$`)
	moneyCueRe                              = regexp.MustCompile(`(?i)\b(?:salary|compensation|pay|base|wage|annual|annually|ote|range|rate|(?:per|a|an) (?:year|hour|month|day))\b|/\s?(?:yr|year|hr|hour|mo|month)\b`)
	fundingRe                               = regexp.MustCompile(`(?i)\b(?:raised|funding|series [a-f]|valuation|revenue|invested|arr|backed|million|billion)\b`)
	moneyRe                                 = moneyPattern()
	plusExtraRe                             = regexp.MustCompile(`(?i)\+\s*(?:equity|bonus|stock|options|commission|benefits)\b.*`)
	moneyScaleRe                            = regexp.MustCompile(`(?i)^\s?(?:m|mm|b|bn|million|billion)\b`)
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
	var codes []string
	for _, c := range config.Currencies {
		codes = append(codes, c.Code)
	}
	cur := `(?:[` + regexp.QuoteMeta(symbols) + `]|\b(?:` + strings.Join(codes, "|") + `)\b)`
	num := `\d{1,3}(?:,\d{3})+|\d+(?:\.\d+)?`
	one := `(?:` + cur + `\s?)?(?:` + num + `)\s?[kK]?`
	return regexp.MustCompile(`(?i)` + one + `\s?(?:-|–|—|to)\s?` + one + `(?:\s?` + cur + `)?|` + cur + `\s?(?:` + num + `)\s?[kK]?|(?:` + num + `)\s?[kK]?\s?` + cur)
}

type conditions struct{}

func (conditions) Name() string { return "conditions" }

func (conditions) Version() int { return 5 }

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
	for _, line := range textLines(doc.Original) {
		if doc.isHeading(line) {
			continue
		}
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
		if !skip[facts.Engagement] {
			if loc := contractRe.FindStringIndex(line.Text); loc != nil {
				out, skip[facts.Engagement] = append(out, vocabFact(facts.Engagement, "contract", "", line.Start+loc[0], line.Start+loc[1])), true
			} else if loc := employmentRe.FindStringIndex(line.Text); loc != nil && section != SectionProcess {
				out, skip[facts.Engagement] = append(out, vocabFact(facts.Engagement, "employment", "", line.Start+loc[0], line.Start+loc[1])), true
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
		if !skip[facts.Place] && section != SectionProcess {
			out = append(out, placesIn(line)...)
		}
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
	for _, loc := range moneyRe.FindAllStringIndex(line.Text, -1) {
		seg := line.Text[loc[0]:loc[1]]
		sentence := sentenceAround(line.Text, loc[0])
		if moneyScaleRe.MatchString(line.Text[loc[1]:]) || fundingRe.MatchString(sentence) {
			continue
		}
		m := ParseMoney(seg, sentence)
		if m == nil || m.Currency == "" || m.Min <= 0 && m.Max <= 0 {
			continue
		}
		if !moneyCueRe.MatchString(sentence) && !(m.Min > 0 && m.Max > m.Min && m.Max >= 1000) {
			continue
		}
		b, _ := json.Marshal(m)
		return vocabFact(facts.Money, string(b), "", line.Start+loc[0], line.Start+loc[1]), true
	}
	return facts.Fact{}, false
}

func sentenceAround(text string, at int) string {
	start := max(strings.LastIndex(text[:at], ". ")+1, strings.LastIndex(text[:at], "|")+1)
	end := len(text)
	for _, stop := range []string{". ", "|"} {
		if i := strings.Index(text[at:], stop); i >= 0 {
			end = min(end, at+i+1)
		}
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
	for _, loc := range remoteRe.FindAllStringIndex(text, -1) {
		after := strings.ToLower(text[loc[1]:])
		if !remoteNoiseRe.MatchString(text[loc[0]:loc[1]]) && !remoteNotRe.MatchString(text[:loc[0]]) && !strings.HasPrefix(after, "-friendly") && !strings.HasPrefix(after, " friendly") {
			add("remote", loc[0], loc[1])
			break
		}
	}
	if loc := distributedRe.FindStringIndex(text); loc != nil {
		add("remote", loc[0], loc[1])
	}
	for _, loc := range hybridRe.FindAllStringIndex(text, -1) {
		if !hybridNoiseRe.MatchString(text[loc[0]:loc[1]]) && !remoteNotRe.MatchString(text[:loc[0]]) {
			add("hybrid", loc[0], loc[1])
			break
		}
	}
	days := false
	if m := daysRe.FindStringSubmatchIndex(text); m != nil {
		n := m[2]
		if n < 0 {
			n = m[4]
		}
		days = true
		add(dayCounts[strings.ToLower(text[n:n+len(strings.Fields(text[n:])[0])])], m[0], m[1])
	}
	if !days {
		for _, loc := range onsiteRe.FindAllStringIndex(text, -1) {
			if !onsiteNoiseRe.MatchString(text[loc[1]:]) && !remoteNotRe.MatchString(text[:loc[0]]) {
				add("onsite", loc[0], loc[1])
				break
			}
		}
	}
	if strings.Count(text, "|") >= 2 {
		at := 0
		for _, seg := range strings.Split(text, "|") {
			if mode, ok := ParseMode(seg); ok {
				add(mode, at+strings.Index(seg, strings.TrimSpace(seg)), at+len(strings.TrimRight(seg, " ")))
			}
			at += len(seg) + 1
		}
	}
	return cues
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
		out = append(out, vocabFact(facts.Place, it.label, "", line.Start+from+it.start, line.Start+from+it.end))
	}
	return out
}

func placesIn(line Line) []facts.Fact {
	var out []facts.Fact
	for _, m := range placeCueRe.FindAllStringSubmatchIndex(line.Text, -1) {
		group := 2
		if m[2] < 0 {
			group = 4
		}
		if m[group] >= 0 {
			out = append(out, placeFacts(line, line.Text[m[group]:m[group+1]], m[group])...)
		}
	}
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
