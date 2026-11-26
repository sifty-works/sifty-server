package shortlistcheck_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/shortlistcheck"
)

func facts() shortlistcheck.ProfileFacts {
	return shortlistcheck.ProfileFacts{Skills: []candidate.Skill{{Name: "Go", Level: "Proficient"}}}
}

func vacancy() fit.Vacancy {
	return fit.Vacancy{Title: "Backend Engineer", Full: true, Analysed: true, Requirements: []fit.Requirement{
		{Text: "Go", Level: "required"},
		{Text: "Fluent German", Level: "required"},
		{Text: "Mentoring experience", Level: "preferred"},
	}}
}

const evidence = "LANGUAGES\n- German (C1)\n- English (Native)\n\nSKILLS\n- Go (Proficient)"

func reply(answers ...map[string]string) models.Response {
	b, _ := json.Marshal(map[string]any{"answers": answers})
	return models.Response{Text: string(b)}
}

func answer(requirement, verdict, excerpt string) map[string]string {
	return map[string]string{"requirement": requirement, "verdict": verdict, "reason": "The data says so.", "excerpt": excerpt}
}

func run(t *testing.T, fm *models.FakeModel) shortlistcheck.Check {
	t.Helper()
	got, err := shortlistcheck.Checker{Model: fm, Text: "We hire a backend engineer. German is required."}.Check(context.Background(), facts(), evidence, vacancy())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return got
}

func TestOnlyRequirementsCodeLeftUndecidedAreAskedAndLocatedAnswersKept(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply(answer("Fluent German", "met", "German (C1)")))
	got := run(t, fm)

	calls := fm.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	asked := calls[0].Prompt[strings.LastIndex(calls[0].Prompt, "\nREQUIREMENTS TO DECIDE\n"):strings.LastIndex(calls[0].Prompt, "\nVACANCY FACTS")]
	if !strings.Contains(asked, "Fluent German") || !strings.Contains(asked, "Mentoring experience") {
		t.Errorf("undecided requirements are not asked: %q", asked)
	}
	if strings.Contains(asked, "- Go ") {
		t.Errorf("a requirement code decided was sent to the model: %q", asked)
	}
	if !strings.Contains(calls[0].Prompt, "We hire a backend engineer") || !strings.Contains(calls[0].Prompt, evidence) {
		t.Error("prompt lacks the vacancy text or the candidate evidence")
	}
	if len(got.Answers) != 1 || got.Answers[0].Requirement != "Fluent German" || got.Answers[0].Verdict != "✓" || got.Answers[0].Excerpt != "German (C1)" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestAnswerIsDroppedWhenItsExcerptIsNotInTheEvidence(t *testing.T) {
	fm := &models.FakeModel{}
	for range 2 {
		fm.AddReply(reply(answer("Fluent German", "met", "Native German speaker since birth")))
	}
	if got := run(t, fm); len(got.Answers) != 0 {
		t.Errorf("answer with an invented excerpt was kept: %+v", got.Answers)
	}
	if n := len(fm.Calls()); n != 2 {
		t.Errorf("a reply whose every answer failed validation was not asked again: calls = %d", n)
	}
}

func TestAnswerIsDroppedWhenItsRequirementIsNotAnUndecidedOne(t *testing.T) {
	fm := &models.FakeModel{}
	for range 2 {
		fm.AddReply(reply(answer("Go", "met", "Go (Proficient)"), answer("Rust", "not_met", "Go (Proficient)")))
	}
	if got := run(t, fm); len(got.Answers) != 0 {
		t.Errorf("answers about decided or unknown requirements were kept: %+v", got.Answers)
	}
}

func TestRequirementAndExcerptAreMatchedFoldedAndExcerptIsReturnedAsInTheEvidence(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply(answer("Fluent\u00a0 German", "met", "English  (Native)")))
	got := run(t, fm)
	if len(got.Answers) != 1 || got.Answers[0].Requirement != "Fluent German" || got.Answers[0].Excerpt != "English (Native)" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestNoModelCallWhenCodeDecidedEveryRequirement(t *testing.T) {
	fm := &models.FakeModel{}
	v := vacancy()
	v.Requirements = v.Requirements[:1]
	got, err := shortlistcheck.Checker{Model: fm}.Check(context.Background(), facts(), evidence, v)
	if err != nil || len(got.Answers) != 0 || len(fm.Calls()) != 0 {
		t.Errorf("answers=%v err=%v calls=%d", got.Answers, err, len(fm.Calls()))
	}
}

func TestRequirementCopiedWithTheLevelAndAlternativesThePromptPrintsIsStillMatched(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply(answer("Fluent German [required]", "met", "German (C1)")))
	if got := run(t, fm); len(got.Answers) != 1 || got.Answers[0].Requirement != "Fluent German" {
		t.Errorf("answers = %+v", got.Answers)
	}

	fm2 := &models.FakeModel{}
	fm2.AddReply(reply(answer("Mentoring experience (any of: coaching, mentoring) [preferred]", "met", "German (C1)")))
	if got := run(t, fm2); len(got.Answers) != 1 || got.Answers[0].Requirement != "Mentoring experience" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestRequirementWhoseOwnTextEndsInBracketsIsMatchedWhole(t *testing.T) {
	v := vacancy()
	v.Requirements = append(v.Requirements, fit.Requirement{Text: "Continuous Threat Exposure Management (CTEM)", Level: "preferred"})
	fm := &models.FakeModel{}
	fm.AddReply(reply(answer("Continuous Threat Exposure Management (CTEM)", "met", "German (C1)")))
	got, err := shortlistcheck.Checker{Model: fm, Text: "We hire a backend engineer."}.Check(context.Background(), facts(), evidence, v)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(got.Answers) != 1 || got.Answers[0].Requirement != "Continuous Threat Exposure Management (CTEM)" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestExcerptIsShownWithoutTheListMarkerAndOnOneLine(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply(answer("Fluent German", "met", "- German (C1)\n- English (Native)")))
	got := run(t, fm)
	if len(got.Answers) != 1 || got.Answers[0].Excerpt != "German (C1) - English (Native)" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestAReplyThatDecidesNothingIsAskedOnceMoreAndASecondOneIsKept(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply())
	fm.AddReply(reply(answer("Fluent German", "met", "German (C1)")))
	got := run(t, fm)
	if n := len(fm.Calls()); n != 2 {
		t.Fatalf("model calls = %d, want 2", n)
	}
	if len(got.Answers) != 1 || got.Answers[0].Requirement != "Fluent German" {
		t.Errorf("answers = %+v", got.Answers)
	}
}

func TestASecondReplyThatDecidesNothingIsTheAnswerAndNothingIsAskedAgain(t *testing.T) {
	fm := &models.FakeModel{}
	fm.AddReply(reply())
	fm.AddReply(reply())
	got := run(t, fm)
	if n := len(fm.Calls()); n != 2 {
		t.Errorf("model calls = %d, want 2", n)
	}
	if len(got.Answers) != 0 {
		t.Errorf("answers = %+v", got.Answers)
	}
}
