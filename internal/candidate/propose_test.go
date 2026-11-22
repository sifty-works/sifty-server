package candidate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/models"
)

func TestProposeRequestNamesEveryKindAndAnswerOutsideTheListIsIgnored(t *testing.T) {
	m := &models.FakeModel{}
	m.AddReply(models.Response{Text: `{"facts":[
		{"field":"skill","value":"Go","quote":"q"},
		{"field":"language","value":"French (B2)","quote":"q"},
		{"field":"targetRole","value":"Backend Engineer","quote":"q"},
		{"field":"experience","value":"Engineer at Acme","quote":"q"},
		{"field":"hobby","value":"Chess","quote":"q"}]}`})
	facts, err := candidate.ProposeFromChunk(context.Background(), m, "text")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 4 {
		t.Fatalf("got %d facts, want 4", len(facts))
	}
	req := m.Calls()[0]
	for _, name := range []string{"skill", "language", "targetRole", "experience"} {
		if !strings.Contains(req.Prompt, "- "+name+": ") {
			t.Errorf("prompt has no rule for %q", name)
		}
	}
	if !strings.Contains(string(req.Schema), `"enum": ["skill","language","targetRole","experience"]`) {
		t.Errorf("schema enum: %s", req.Schema)
	}
}
