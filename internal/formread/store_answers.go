package formread

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
)

const SystemPage = "page"

type Subject struct {
	ID      int64
	Title   string
	Company string
}

func SubjectOf(ctx context.Context, database *db.DB, vacancy int64) (Subject, error) {
	s := Subject{ID: vacancy}
	return s, database.QueryRowContext(ctx, `SELECT title, company FROM vacancy WHERE id=?`, vacancy).Scan(&s.Title, &s.Company)
}

func FindVacancy(ctx context.Context, database *db.DB, canonical func(string) (string, error), page string) (Subject, error) {
	u, _ := url.Parse(page)
	rows, err := database.QueryContext(ctx,
		`SELECT v.id, v.title, v.company, a.value FROM vacancy v LEFT JOIN employer_page e ON e.vacancy=v.id,
		 json_each(json_array(v.url, v.apply_url, e.url)) a WHERE instr(a.value, ?)>0 ORDER BY v.id DESC`, u.Hostname())
	if err != nil {
		return Subject{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var s Subject
		var address string
		if err := rows.Scan(&s.ID, &s.Title, &s.Company, &address); err != nil {
			return Subject{}, err
		}
		if c, err := canonical(address); err == nil && c == page {
			return s, nil
		}
	}
	return Subject{}, rows.Err()
}

func Merge(ctx context.Context, database *db.DB, vacancy int64, page string, fields []Field, at time.Time) ([]Field, error) {
	stored, _, err := Load(ctx, database, vacancy)
	if err != nil {
		return nil, err
	}
	form, old, byKey := stored.Form, map[string]string{}, map[string]Field{}
	if stored.State != StateRead {
		form = Form{URL: page, System: SystemPage}
	}
	for _, f := range form.Fields {
		old[f.ID], byKey[f.ID] = f.Label, f
	}
	keys, out := matchKeys(old, fields), make([]Field, len(fields))
	for i, key := range keys {
		if out[i] = byKey[key]; out[i].ID == "" {
			out[i] = fields[i]
			form.Fields = append(form.Fields, fields[i])
		}
	}
	return out, Save(ctx, database, vacancy, form, StateRead, "", at)
}

func SaveAnswers(ctx context.Context, database *db.DB, form int64, fields []Field) error {
	for _, f := range fields {
		values, _ := json.Marshal(append([]string{}, f.Values...))
		if _, err := database.ExecContext(ctx,
			`UPDATE application_field SET answer=?, source=?, note=?, evidence=?, revision=revision+1
			 WHERE form=? AND key=? AND source<>? AND (answer<>? OR source<>? OR note<>? OR evidence<>?)`,
			string(values), f.Source, f.Note, f.Evidence, form, f.ID, SourceUser, string(values), f.Source, f.Note, f.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func Edit(ctx context.Context, database *db.DB, vacancy int64, key string, values []string, revision int64) error {
	encoded, _ := json.Marshal(append([]string{}, values...))
	res, err := database.ExecContext(ctx,
		`UPDATE application_field SET answer=?, source=?, note='', evidence='', revision=revision+1
		 WHERE form=? AND key=? AND revision=?`,
		string(encoded), SourceUser, vacancy, key, revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.Conflict{Message: "This answer was changed elsewhere"}
	}
	return nil
}
