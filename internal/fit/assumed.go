package fit

import (
	"fmt"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/skills"
)

func Assume(experience []candidate.Experience, now time.Time) map[string]skills.Assumption {
	byGroup := map[string][]candidate.Experience{}
	for _, e := range experience {
		seen := map[string]bool{}
		mentioned := skills.Groups(e.Title + ". " + e.Summary)
		for _, name := range e.Skills {
			mentioned = append(mentioned, skills.Groups(name)...)
		}
		for _, g := range mentioned {
			if !seen[g] {
				seen[g] = true
				byGroup[g] = append(byGroup[g], e)
			}
		}
	}
	stale := now.AddDate(-10, 0, 0)
	out := map[string]skills.Assumption{}
	for g, entries := range byGroup {
		var from []string
		var recent []candidate.Experience
		for _, e := range entries {
			who := e.Company
			if who == "" {
				who = e.Title
			}
			if candidate.EndedBefore(e, stale) {
				from = append(from, who+" · "+e.Start+"–"+e.End)
				continue
			}
			recent = append(recent, e)
			from = append(from, who+" · "+spanText(candidate.CountedMonths([]candidate.Experience{e}, now)))
		}
		level := skills.LevelForYears(candidate.CountedMonths(recent, now) / 12)
		if level == "" {
			level = skills.LevelForYears(1)
		}
		out[g] = skills.Assumption{Level: level, From: strings.Join(from, "; ")}
	}
	return out
}

func spanText(months int) string {
	switch years := months / 12; years {
	case 0:
		return "under a year"
	case 1:
		return "1 year"
	default:
		return fmt.Sprintf("%d years", years)
	}
}
