package answer

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/server"
)

type FillOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FillField struct {
	Key      string       `json:"key"`
	Label    string       `json:"label"`
	Kind     string       `json:"kind"`
	Required bool         `json:"required"`
	Format   string       `json:"format,omitempty"`
	Options  []FillOption `json:"options"`
	Value    string       `json:"value,omitempty"`
	Values   []string     `json:"values,omitempty"`
}

type FillRequest struct {
	URL    string      `json:"url"`
	Title  string      `json:"title"`
	Text   string      `json:"text"`
	Fields []FillField `json:"fields"`
}

type FillVacancy struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Company string `json:"company"`
}

type FillAnswer struct {
	Key    string   `json:"key"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
	Source string   `json:"source"`
	Note   string   `json:"note"`
}

type FillLeft struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type FillResponse struct {
	Form    int64        `json:"form"`
	Vacancy *FillVacancy `json:"vacancy"`
	State   string       `json:"state"`
	Answers []FillAnswer `json:"answers"`
	Left    []FillLeft   `json:"left"`
	Open    string       `json:"open"`
}

var formKinds = map[string]string{
	"text": formread.KindShortText, "email": "email", "tel": "phone", "url": "link", "number": "number",
	"date": "date", "textarea": "long_text", "select": formread.KindChoice, "radio": formread.KindChoice,
	"combobox": formread.KindChoice, "checkboxes": formread.KindMultipleChoice, "checkbox": formread.KindYesNo, "yesno": formread.KindYesNo,
	"file": formread.KindFile,
}

func Apply(ctx context.Context, database *db.DB, a Answerer, v Vacancy, vacancy int64, f formread.Form) (int, []formread.Field, error) {
	have := Answers{}
	for _, fl := range f.Fields {
		if fl.Source == formread.SourceUser {
			have[fl.ID] = Answer{Values: fl.Values, Source: SourceUser, Note: fl.Note, Evidence: fl.Evidence}
		}
	}
	got, err := a.Answer(ctx, v, f, have)
	if err != nil {
		return 0, nil, err
	}
	answered, out := 0, make([]formread.Field, len(f.Fields))
	for i, fl := range f.Fields {
		g := got[fl.ID]
		if len(g.Values) > 0 {
			answered++
		}
		out[i] = formread.Field{ID: fl.ID, Values: g.Values, Source: string(g.Source), Note: g.Note, Evidence: g.Evidence}
	}
	if vacancy > 0 {
		err = formread.SaveAnswers(ctx, database, vacancy, out)
	}
	return answered, out, err
}

func RegisterExtensionRoutes(mux *http.ServeMux, logger *slog.Logger, database *db.DB, a Answerer, canonical func(string) (string, error)) {
	server.Handle(mux, "POST /api/extension/fill", logger, func(r *http.Request, req FillRequest) (FillResponse, error) {
		return fill(r.Context(), database, a, canonical, req)
	})
	server.Handle(mux, "GET /api/extension/fill/{form}", logger, func(r *http.Request, _ struct{}) (FillResponse, error) {
		id, _ := strconv.ParseInt(r.PathValue("form"), 10, 64)
		stored, ok, err := formread.Load(r.Context(), database, id)
		if err != nil || !ok {
			return FillResponse{}, cmp.Or(err, error(apperr.NotFound{Message: "Nothing stored for this page."}))
		}
		subject, err := formread.SubjectOf(r.Context(), database, id)
		return reply(subject, stored.Fields), err
	})
}

func fill(ctx context.Context, database *db.DB, a Answerer, canonical func(string) (string, error), req FillRequest) (FillResponse, error) {
	page, err := canonical(req.URL)
	if err != nil || len(req.Fields) == 0 {
		return FillResponse{}, apperr.Invalid{Message: "The page has no address or no form fields."}
	}
	subject, err := formread.FindVacancy(ctx, database, canonical, page)
	if err != nil {
		return FillResponse{}, err
	}
	fields := make([]formread.Field, len(req.Fields))
	for i, pf := range req.Fields {
		options := make([]string, len(pf.Options))
		for j, o := range pf.Options {
			options[j] = o.Label
		}
		fields[i] = formread.Field{ID: pf.Key, Label: pf.Label, Kind: cmp.Or(formKinds[pf.Kind], formread.KindShortText), Required: pf.Required, Options: options}
	}
	vac := Vacancy{Title: subject.Title, Company: subject.Company}
	if subject.ID > 0 {
		if fields, err = formread.Merge(ctx, database, subject.ID, page, fields, time.Now()); err != nil {
			return FillResponse{}, err
		}
	} else {
		vac = Vacancy{Title: req.Title, Text: req.Text}
	}
	_, answers, err := Apply(ctx, database, a, vac, subject.ID, formread.Form{URL: page, Fields: fields})
	if err != nil {
		return FillResponse{}, err
	}
	for i := range answers {
		answers[i].ID = req.Fields[i].Key
	}
	return reply(subject, answers), nil
}

func reply(subject formread.Subject, fields []formread.Field) FillResponse {
	out := FillResponse{Form: subject.ID, State: "done", Answers: []FillAnswer{}, Left: []FillLeft{}, Open: "/"}
	if subject.ID > 0 {
		out.Vacancy = &FillVacancy{ID: subject.ID, Title: subject.Title, Company: subject.Company}
	}
	for _, fl := range fields {
		if len(fl.Values) == 0 {
			out.Left = append(out.Left, FillLeft{Key: fl.ID, Reason: cmp.Or(fl.Note, "Sifty has no answer for this yet")})
			continue
		}
		out.Answers = append(out.Answers, FillAnswer{Key: fl.ID, Value: fl.Values[0], Values: fl.Values, Source: fl.Source, Note: fl.Note})
	}
	return out
}
