package fit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/skills"
)

const ScoringVersion = 8

const current = `(f.inputs IS ? AND f.version IS v.version)`

const Join = `LEFT JOIN fit f ON f.profile=? AND f.vacancy=v.id`

const Columns = `f.state, f.score, f.inputs, f.version IS v.version, f.req_ok, f.req_no, f.req_maybe, f.broken, f.req_near`

const OrderBestFit = `ORDER BY CASE WHEN ` + current + ` AND f.state IS 'scored' THEN f.score - 1000*f.broken ELSE -1 END DESC, v.id ASC`

type Stored struct {
	State                  sql.NullString
	Score, Inputs          sql.NullInt64
	SameVacancyVersion     bool
	ReqOK, ReqNo, ReqMaybe sql.NullInt64
	Broken                 sql.NullBool
	ReqNear                sql.NullInt64
}

func (s *Stored) Dest() []any {
	return []any{&s.State, &s.Score, &s.Inputs, &s.SameVacancyVersion, &s.ReqOK, &s.ReqNo, &s.ReqMaybe, &s.Broken, &s.ReqNear}
}

func (s Stored) Current(inputs int64) bool {
	return s.Inputs.Valid && s.Inputs.Int64 == inputs && s.SameVacancyVersion
}

func Buckets(names []string, inputs int64) (string, []any) {
	scored := current + ` AND f.state IS 'scored'`
	var parts []string
	var args []any
	for _, n := range names {
		p, ok := map[string]string{
			"strong":     fmt.Sprintf("%s AND f.score>=%d", scored, StrongFrom),
			"partial":    fmt.Sprintf("%s AND f.score>=%d AND f.score<%d", scored, PartialFrom, StrongFrom),
			"weak":       fmt.Sprintf("%s AND f.score<%d", scored, PartialFrom),
			"not_scored": "NOT (" + scored + ")",
		}[n]
		if ok {
			parts, args = append(parts, "("+p+")"), append(args, inputs)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func Band(ctx context.Context, database *db.DB, profileID, vacancyID int64, r Rules) (Fit, error) {
	p, err := LoadProfile(ctx, database, profileID, r)
	if err != nil {
		return Fit{}, err
	}
	v, _, _, ok, err := LoadVacancy(ctx, database, vacancyID)
	if err != nil {
		return Fit{}, err
	}
	if !ok {
		return Fit{}, apperr.NotFound{Message: "Role not found."}
	}
	return Score(p.View, v, r), nil
}

type Profile struct {
	ID     int64
	View   ProfileView
	Inputs int64
}

func Inputs(p ProfileView, r Rules) int64 {
	b, _ := json.Marshal(struct {
		V int
		P ProfileView
		R Rules
	}{ScoringVersion, p, r})
	h := fnv.New64a()
	h.Write(b)
	h.Write(skills.VocabBytes)
	return int64(h.Sum64())
}

func LoadProfile(ctx context.Context, database *db.DB, id int64, r Rules) (Profile, error) {
	pd, err := candidate.EffectiveProfile(ctx, database, id)
	if err != nil {
		return Profile{}, err
	}
	me, err := candidate.LoadMe(ctx, database)
	if err != nil {
		return Profile{}, err
	}
	v := ProfileView{
		Goals:          candidate.Goals(pd),
		Authorizations: me.WorkAuthorizations,
		Languages:      me.Languages,
		Skills:         me.Skills,
		AbsentSkills:   me.AbsentSkills,
		Confirmed:      me.ConfirmedFacts,
		DerivedYears:   candidate.DeriveYears(me.Experience, time.Now()),
		Today:          time.Now(),
	}
	return Profile{id, v, Inputs(v, r)}, nil
}

func ProfileIDs(ctx context.Context, database *db.DB) ([]int64, error) {
	rows, err := database.QueryContext(ctx, `SELECT id FROM profile ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("fit: profiles: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("fit: profiles: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func LoadProfiles(ctx context.Context, database *db.DB, r Rules) ([]Profile, error) {
	ids, err := ProfileIDs(ctx, database)
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, id := range ids {
		p, err := LoadProfile(ctx, database, id, r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func LoadVacancy(ctx context.Context, database *db.DB, id int64) (Vacancy, string, int64, bool, error) {
	batch, err := catalog.New(database, nil).VacancyTexts(ctx, id-1, 1)
	if err != nil || len(batch) == 0 || batch[0].ID != id {
		return Vacancy{}, "", 0, false, err
	}
	t := batch[0]
	html, err := catalog.Body(t.Body)
	if err != nil {
		return Vacancy{}, "", 0, false, fmt.Errorf("fit: vacancy %d: %w", t.ID, err)
	}
	sets, err := facts.Load(ctx, database, id)
	if err != nil {
		return Vacancy{}, "", 0, false, err
	}
	row := listed{id: t.ID, version: t.Version, title: t.Title, textState: t.TextState}
	return FromFacts(row.title, row.full(), sets[id]), listing.PlainText(html), t.Version, true, nil
}

type listed struct {
	id, version                              int64
	title, location, tags, fields, textState string
	factsKey                                 string
}

func (l listed) full() bool { return listing.TextState(l.textState) == listing.TextFull }

func (l listed) key() int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\x00%s", l.version, l.title, l.location, l.tags, l.fields, l.factsKey)
	return int64(h.Sum64())
}

func listAfter(ctx context.Context, database *db.DB, after int64, limit int) ([]listed, map[int64]facts.Set, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT v.id, v.version, v.title, v.location, v.tags, v.source_fields, v.text_state
		 FROM vacancy v WHERE v.id>? ORDER BY v.id LIMIT ?`, after, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("fit: vacancies: %w", err)
	}
	defer rows.Close()
	var out []listed
	var ids []int64
	for rows.Next() {
		var l listed
		if err := rows.Scan(&l.id, &l.version, &l.title, &l.location, &l.tags, &l.fields, &l.textState); err != nil {
			return nil, nil, fmt.Errorf("fit: vacancies: scan: %w", err)
		}
		out, ids = append(out, l), append(ids, l.id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("fit: vacancies: %w", err)
	}
	sets, err := facts.Load(ctx, database, ids...)
	for i := range out {
		out[i].factsKey = sets[out[i].id].Key
	}
	return out, sets, err
}

func RecomputeVacancy(ctx context.Context, database *db.DB, vacancyID int64, r Rules) error {
	profiles, err := LoadProfiles(ctx, database, r)
	if err != nil {
		return err
	}
	batch, sets, err := listAfter(ctx, database, vacancyID-1, 1)
	if err != nil || len(batch) == 0 || batch[0].id != vacancyID {
		return err
	}
	_, err = writeBatch(ctx, database, batch, sets, map[int64][]Profile{vacancyID: profiles}, r)
	return err
}

func Recompute(ctx context.Context, database *db.DB, rules func() Rules, progress func(done, total int, current string)) error {
	for {
		n, err := recomputePass(ctx, database, rules(), progress)
		if err != nil || n == 0 {
			return err
		}
	}
}

func recomputePass(ctx context.Context, database *db.DB, r Rules, progress func(int, int, string)) (int, error) {
	profiles, err := LoadProfiles(ctx, database, r)
	if err != nil {
		return 0, err
	}
	stale, total := map[int64][]Profile{}, 0
	for _, p := range profiles {
		rows, err := database.QueryContext(ctx, `SELECT v.id, v.version, v.title, v.location, v.tags, v.source_fields, `+facts.KeySQL+`,
			`+current+`, f.listing
			FROM vacancy v `+Join,
			p.Inputs, p.ID)
		if err != nil {
			return 0, fmt.Errorf("fit: out of date: %w", err)
		}
		for rows.Next() {
			var l listed
			var ok bool
			var stored sql.NullInt64
			if err := rows.Scan(&l.id, &l.version, &l.title, &l.location, &l.tags, &l.fields, &l.factsKey, &ok, &stored); err != nil {
				rows.Close()
				return 0, fmt.Errorf("fit: out of date: %w", err)
			}
			if !ok || stored.Int64 != l.key() {
				stale[l.id] = append(stale[l.id], p)
				total++
			}
		}
		rows.Close()
	}
	written, waiting := 0, len(stale)
	for after := int64(0); waiting > 0; {
		batch, sets, err := listAfter(ctx, database, after, 200)
		if err != nil || len(batch) == 0 {
			return written, err
		}
		after = batch[len(batch)-1].id
		for _, l := range batch {
			if len(stale[l.id]) > 0 {
				waiting--
			}
		}
		n, err := writeBatch(ctx, database, batch, sets, stale, r)
		if err != nil {
			return written, err
		}
		written += n
		progress(written, total, "")
	}
	return written, nil
}

func writeBatch(ctx context.Context, database *db.DB, batch []listed, sets map[int64]facts.Set, stale map[int64][]Profile, r Rules) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("fit: write: %w", err)
	}
	defer tx.Rollback()
	n := int64(0)
	for _, l := range batch {
		if len(stale[l.id]) == 0 {
			continue
		}
		read := FromFacts(l.title, l.full(), sets[l.id])
		for _, p := range stale[l.id] {
			f := Score(p.View, read, r)
			var score, reqOK, reqNear, reqNo, reqMaybe any
			if f.State == Scored {
				score = f.Score
			}
			if f.ReqRead {
				reqOK, reqNear, reqNo, reqMaybe = f.ReqOK, f.ReqNear, f.ReqNo, f.ReqMaybe
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO fit (profile, vacancy, version, inputs, listing, state, score, req_ok, req_no, req_maybe, broken, req_near)
				SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM profile WHERE id=?) AND EXISTS (SELECT 1 FROM vacancy WHERE id=?)
				ON CONFLICT (profile, vacancy) DO UPDATE SET version=excluded.version, inputs=excluded.inputs,
				listing=excluded.listing, state=excluded.state, score=excluded.score,
				req_ok=excluded.req_ok, req_no=excluded.req_no, req_maybe=excluded.req_maybe, broken=excluded.broken, req_near=excluded.req_near`,
				p.ID, l.id, l.version, p.Inputs, l.key(), f.State, score, reqOK, reqNo, reqMaybe, f.Broken, reqNear, p.ID, l.id)
			if err != nil {
				return 0, fmt.Errorf("fit: write %d: %w", l.id, err)
			}
			k, _ := res.RowsAffected()
			n += k
		}
	}
	return int(n), tx.Commit()
}
