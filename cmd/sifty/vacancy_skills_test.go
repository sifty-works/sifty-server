package main

import (
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/skills"
)

func TestSkillLevelsOfTheProfileEditorAreTheLevelsTheVocabularyReads(t *testing.T) {
	d := openTestDB(t)
	base := newRouteServer(t, d, runner.New(d, nil, time.Now, slog.Default()))
	ref, status := apiGet(t, base, "/api/reference")
	if status != 200 {
		t.Fatalf("GET /api/reference: status %d", status)
	}
	options, _ := ref["options"].(map[string]any)
	var got []string
	for _, o := range options["skillLevels"].([]any) {
		got = append(got, o.(map[string]any)["value"].(string))
	}
	if want := skills.Levels(); !reflect.DeepEqual(got, want) {
		t.Errorf("skill levels offered to the user %v, levels the vocabulary reads %v", got, want)
	}
}
