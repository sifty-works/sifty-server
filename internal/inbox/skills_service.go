package inbox

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
)

type skillsResult struct {
	notice string
	added  bool
}

const noChange = "Nothing new to add; your data is already up to date."

func applySkillsDecision(ctx context.Context, database *db.DB, vacancyID, profileID int64, body SkillsDecisionReq, r fit.Rules) (skillsResult, error) {
	f, err := fit.Band(ctx, database, profileID, vacancyID, r)
	if err != nil {
		return skillsResult{}, err
	}
	me, err := candidate.LoadMe(ctx, database)
	if err != nil {
		return skillsResult{}, err
	}
	var notice string
	var changed bool
	if body.Level != "" {
		skill, err := namedSkill(f, body)
		if err != nil {
			return skillsResult{}, err
		}
		me, changed = fit.SetLevel(skill, body.Level, me)
		notice = levelNotice(skill, body.Level)
	} else {
		if body.ReqIndex == nil || body.Have == nil {
			return skillsResult{}, apperr.Invalid{Message: "Give a skill with a level, or a requirement with have."}
		}
		i := slices.IndexFunc(f.Requirements, func(l fit.RequirementLine) bool { return l.Index == *body.ReqIndex })
		if i < 0 {
			return skillsResult{}, apperr.Invalid{Message: "No such requirement row; the skills of a line are answered by level."}
		}
		text := f.Requirements[i].Text
		me, changed = fit.ConfirmRow(text, *body.Have, me)
		statement := "something you do not have"
		if *body.Have {
			statement = "something you have"
		}
		notice = fmt.Sprintf("Added %q to Confirmed by you as %s (change in your profile).", text, statement)
	}
	if !changed {
		return skillsResult{notice: noChange}, nil
	}
	if err := candidate.SaveMe(ctx, database, me); err != nil {
		return skillsResult{}, err
	}
	if err := fit.RecomputeVacancy(ctx, database, vacancyID, r); err != nil {
		return skillsResult{}, err
	}
	return skillsResult{notice: notice, added: true}, nil
}

func namedSkill(f fit.Fit, body SkillsDecisionReq) (string, error) {
	if !fit.IsLevel(body.Level) {
		return "", apperr.Invalid{Message: "Level must be one of the skill levels or No."}
	}
	for _, e := range f.Skills {
		for _, s := range e.Skills {
			if strings.EqualFold(s.Name, body.Skill) {
				return s.Name, nil
			}
		}
	}
	return "", apperr.Invalid{Message: "The vacancy does not name that skill."}
}

func levelNotice(skill, level string) string {
	if level == "No" {
		return fmt.Sprintf("Set %s to skills you do not have (change in your profile).", skill)
	}
	return fmt.Sprintf("Set %s to %s in your skills (change in your profile).", skill, level)
}
