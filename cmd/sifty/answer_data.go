package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/sifty-works/sifty-server/internal/answer"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/places"
)

type answerData struct{ database *db.DB }

type knownValues map[string][]answer.Value

var choiceSynonyms = map[string][]string{"man": {"male"}, "woman": {"female"}}

func (k knownValues) put(key, text string, match ...string) {
	if text = strings.TrimSpace(text); text != "" {
		k[key] = append(k[key], answer.Value{Text: text, Match: match})
	}
}

func (k knownValues) putChoice(key string, options []candidate.OptionItem, value, description string) {
	if value == "" {
		return
	}
	label := value
	for _, o := range options {
		if o.Value == value {
			label = o.Label
		}
	}
	text := label
	if value == "self_describe" && description != "" {
		text = description
	}
	k[key] = append(k[key], answer.Value{Text: text, Match: append([]string{value, label}, choiceSynonyms[value]...)})
}

func (d answerData) Known(ctx context.Context) (answer.Known, error) {
	me, err := candidate.LoadMe(ctx, d.database)
	if err != nil {
		return answer.Known{}, err
	}
	profileID, err := candidate.ActiveProfileID(ctx, d.database)
	if err != nil {
		return answer.Known{}, err
	}
	prof, err := candidate.EffectiveProfile(ctx, d.database, profileID)
	if err != nil {
		return answer.Known{}, err
	}
	items, err := candidate.QAItems(ctx, d.database)
	if err != nil {
		return answer.Known{}, err
	}
	docs, err := candidate.EvidenceDocuments(ctx, d.database)
	if err != nil {
		return answer.Known{}, err
	}
	k := knownValues{}
	legal := strings.Fields(orElse(me.LegalName, me.DisplayName))
	if len(legal) > 0 {
		k.put("full_name", strings.Join(legal, " "))
		k.put("first_name", legal[0])
		k.put("last_name", strings.Join(legal[1:], " "))
	}
	if shown := strings.Fields(orElse(me.DisplayName, me.LegalName)); len(shown) > 0 {
		k.put("preferred_name", shown[0])
	}
	k.put("email", me.Email)
	k.put("phone", me.Phone)
	k.put("linkedin", me.LinkedIn)
	k.put("github", me.GitHub)
	k.put("x", me.X)
	k.put("website", me.Website)
	if loc := me.CurrentLocation; loc != nil {
		k.put("city", loc.City)
		k.put("country", loc.Country)
		k.put("location", joinFilled(", ", loc.City, loc.Country), loc.City, loc.Country)
	}
	var latest *candidate.Experience
	for i := range me.Experience {
		e := &me.Experience[i]
		if e.Current {
			latest = e
			break
		}
		if latest == nil || e.Start > latest.Start {
			latest = e
		}
	}
	if latest != nil {
		k.put("current_company", latest.Company)
		k.put("current_title", latest.Title)
	}
	for _, l := range me.Languages {
		k.put("languages", l.Name)
	}
	if a := me.Availability; a != nil {
		if n := a.NoticeValue; n != nil && a.NoticeUnit != "" {
			switch {
			case *n == 0:
				k.put("notice_period", "Immediately")
			case *n == 1:
				k.put("notice_period", "1 "+a.NoticeUnit)
			default:
				k.put("notice_period", fmt.Sprintf("%d %ss", *n, a.NoticeUnit))
			}
		}
		k.put("start_date", a.StartDate)
	}
	if c := prof.Compensation; c != nil && c.CashTarget != nil {
		per := ""
		if c.Period != "" {
			per = "per " + c.Period
		}
		k.put("salary", joinFilled(" ", strconv.FormatFloat(*c.CashTarget, 'f', -1, 64), c.Currency, per))
	}
	if dm := me.Demographics; dm != nil {
		o := candidate.Options()
		k.putChoice("gender", o.Gender, dm.Gender, dm.GenderDescription)
		for _, r := range dm.Race {
			k.putChoice("race", o.Race, r, dm.RaceDescription)
		}
		k.putChoice("ethnicity", o.Ethnicity, dm.Ethnicity, "")
		if len(k["ethnicity"]) > 0 {
			k["ethnicity"][0].Match = append(k["ethnicity"][0].Match, map[string]string{"hispanic_or_latino": "yes", "not_hispanic_or_latino": "no"}[dm.Ethnicity])
		}
		k.putChoice("disability", o.Disability, dm.Disability, "")
		k.putChoice("veteran", o.VeteranStatus, dm.VeteranStatus, "")
		k.putChoice("criminal_record", o.CriminalRecord, dm.CriminalRecord, "")
	}
	residence := ""
	if loc := me.CurrentLocation; loc != nil {
		residence = loc.Country
	}
	known := answer.Known{Contacts: []string{me.Email, me.Phone, me.LinkedIn, me.GitHub, me.X, me.Website}, Names: []string{me.LegalName, me.DisplayName}, Values: k, Evidence: candidate.EvidenceText(me, prof, docs), Authorized: authorized(me.WorkAuthorizations, residence)}
	for _, q := range items {
		known.QA = append(known.QA, answer.QA{Question: q.Question, Answer: q.Answer})
	}
	return known, nil
}

func joinFilled(sep string, parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), sep)
}

func orElse(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func authorized(auths []candidate.WorkAuthorization, residence string) func(label string) (bool, string, bool) {
	return func(label string) (bool, string, bool) {
		auths := slices.DeleteFunc(slices.Clone(auths), func(a candidate.WorkAuthorization) bool {
			return a.Status != "eligible" && a.Status != "need_visa"
		})
		name, named := placesIn(label)
		if name == "" && answer.AsksResidence(label) {
			if name, named = strings.TrimSpace(residence), places.Codes(residence); len(named) == 0 {
				return false, "", false
			}
		}
		if name == "" {
			return len(auths) == 1 && auths[0].Status == "eligible", "", len(auths) == 1
		}
		if len(auths) == 0 {
			return false, "", false
		}
		have := make([]places.Authorization, len(auths))
		for i, a := range auths {
			have[i] = places.Authorization{Country: a.Country, Eligible: a.Status == "eligible"}
		}
		eligible, listed := places.Authorized(have, named)
		if eligible {
			return true, "", true
		}
		if listed {
			return false, "", true
		}
		return false, "My data has no work authorization for " + name, true
	}
}

func placesIn(label string) (first string, codes []string) {
	words := strings.Fields(label)
	for i := 0; i < len(words); i++ {
		for n := min(4, len(words)-i); n > 0; n-- {
			phrase := strings.Trim(strings.Join(words[i:i+n], " "), `?,;:!"'()`)
			cs := places.Codes(phrase)
			if len(cs) == 0 {
				phrase = strings.TrimSuffix(phrase, ".")
				cs = places.Codes(phrase)
			}
			if len(cs) == 0 || !properName(phrase) || (len(strings.ReplaceAll(phrase, ".", "")) <= 3 && phrase != strings.ToUpper(phrase)) {
				continue
			}
			codes = append(codes, cs...)
			if first == "" {
				first = phrase
			}
			i += n - 1
			break
		}
	}
	return first, codes
}

func properName(phrase string) bool {
	for i, w := range strings.Fields(phrase) {
		if r := []rune(w)[0]; !unicode.IsUpper(r) && (i == 0 || (w != "of" && w != "and" && w != "the")) {
			return false
		}
	}
	return true
}
