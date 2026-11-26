package inbox

import (
	"context"
	"fmt"
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

func applySkillsDecision(ctx context.Context, database *db.DB, vacancyID int64, reqIndex int, have bool, chosenSkill string, r fit.Rules) (skillsResult, error) {
	vr, _, _, ok, err := fit.LoadVacancy(ctx, database, vacancyID)
	if err != nil {
		return skillsResult{}, err
	}
	if !ok {
		return skillsResult{}, apperr.NotFound{Message: "Role not found."}
	}
	if reqIndex < 0 || reqIndex >= len(vr.Requirements) {
		return skillsResult{}, apperr.Invalid{Message: "Requirement index out of range."}
	}
	me, err := candidate.LoadMe(ctx, database)
	if err != nil {
		return skillsResult{}, err
	}
	ans := fit.Answer(vr.Requirements[reqIndex].Text, have, chosenSkill, me)
	changed := len(ans.AddedSkills)+len(ans.MovedSkills)+len(ans.AddedConfirmed) > 0
	if changed {
		if err := candidate.SaveMe(ctx, database, ans.Me); err != nil {
			return skillsResult{}, err
		}
		if err := fit.RecomputeVacancy(ctx, database, vacancyID, r); err != nil {
			return skillsResult{}, err
		}
	}
	return skillsResult{notice: buildNotice(ans.AddedSkills, ans.MovedSkills, ans.AddedConfirmed, have), added: changed}, nil
}

func buildNotice(addedSkills, movedSkills, confirmedItems []string, have bool) string {
	if len(addedSkills)+len(movedSkills)+len(confirmedItems) == 0 {
		return "Nothing new to add; your data is already up to date."
	}
	var parts []string
	for _, item := range addedSkills {
		if have {
			parts = append(parts, fmt.Sprintf("Added %q to your skills (level Proficient; change in your profile).", item))
		} else {
			parts = append(parts, fmt.Sprintf("Added %q to skills you do not have (change in your profile).", item))
		}
	}
	for _, item := range movedSkills {
		if have {
			parts = append(parts, fmt.Sprintf("Moved %q from skills you do not have to your skills (level Proficient; change in your profile).", item))
		} else {
			parts = append(parts, fmt.Sprintf("Moved %q from your skills to skills you do not have (change in your profile).", item))
		}
	}
	statement := "something you do not have"
	if have {
		statement = "something you have"
	}
	for _, item := range confirmedItems {
		parts = append(parts, fmt.Sprintf("Added %q to Confirmed by you as %s (change in your profile).", item, statement))
	}
	return strings.Join(parts, " ")
}
