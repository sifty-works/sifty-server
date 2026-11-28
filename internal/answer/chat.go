package answer

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/models"
)

const chatWindow = 10

var errNoComment = errors.New("answer: the model's reply has no comment")

type Chatter struct{ Model models.Model }

type Turn struct {
	Text   string
	Field  string
	Values []string
}

type chatReply struct {
	Comment string   `json:"comment"`
	Field   string   `json:"field"`
	Values  []string `json:"values"`
}

type chatQuestion struct {
	question
	Values []string `json:"values"`
}

func (c Chatter) Reply(ctx context.Context, v Vacancy, k Known, open []formread.Field, history []ChatMessage, turn Turn) (ChatMessage, error) {
	task := models.NamedTask("rewrite_answer")
	questions := make([]chatQuestion, len(open))
	for i, fl := range open {
		questions[i] = chatQuestion{question{fl.ID, fl.Label, fl.Kind, fl.Required, fl.Options}, append([]string{}, fl.Values...)}
	}
	asked, _ := json.Marshal(questions)
	screen, _ := json.Marshal(append([]string{}, turn.Values...))
	var sb strings.Builder
	sb.WriteString("\nQUESTIONS OF THE FORM\n" + string(asked) + "\n\nCONVERSATION\n")
	for _, m := range history[max(0, len(history)-chatWindow):] {
		if m.Role == RoleUser {
			sb.WriteString("User: " + m.Text + "\n")
			continue
		}
		sb.WriteString("Assistant")
		if len(m.Proposal) > 0 {
			sb.WriteString(" (rewrote " + m.Field + ": " + strings.Join(m.Proposal, " ") + ")")
		}
		sb.WriteString(": " + m.Text + "\n")
	}
	sb.WriteString("\nMESSAGE\nField: " + turn.Field + "\nOn screen: " + string(screen) + "\n" + turn.Text + "\n")
	var reply chatReply
	if _, err := task.Ask(ctx, c.Model, task.Prompt+"\n"+Context(v, k)+scrubber(k)(sb.String()), models.Asks, &reply); err != nil {
		return ChatMessage{}, err
	}
	comment := strings.TrimSpace(reply.Comment)
	if comment == "" {
		return ChatMessage{}, errNoComment
	}
	out := ChatMessage{Role: RoleModel, Text: comment, Field: turn.Field}
	if i := slices.IndexFunc(open, func(fl formread.Field) bool { return fl.ID == reply.Field }); i >= 0 {
		if proposal := proposed(open[i], reply.Values); len(proposal) > 0 {
			out.Field, out.Proposal = reply.Field, proposal
		}
	}
	return out, nil
}

func proposed(fl formread.Field, values []string) []string {
	var out []string
	for _, s := range values {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 || !isChoice(fl.Kind, fl.Options) {
		if len(out) > 1 {
			out = []string{strings.Join(out, " ")}
		}
		return out
	}
	wanted := make([]Value, len(out))
	for i, s := range out {
		wanted[i] = Value{Text: s}
	}
	picked, _ := pick(fl.Kind, fl.Options, wanted)
	return picked
}
