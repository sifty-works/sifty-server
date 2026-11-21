package candidate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/industry"
	"github.com/sifty-works/sifty-server/internal/server"
)

type MePatchReq struct {
	DisplayName        *string              `json:"displayName,omitempty"`
	LegalName          *string              `json:"legalName,omitempty"`
	Email              *string              `json:"email,omitempty"`
	Phone              *string              `json:"phone,omitempty"`
	LinkedIn           *string              `json:"linkedin,omitempty"`
	GitHub             *string              `json:"github,omitempty"`
	X                  *string              `json:"x,omitempty"`
	Website            *string              `json:"website,omitempty"`
	Skills             *[]Skill             `json:"skills,omitempty"`
	AbsentSkills       *[]AbsentSkill       `json:"absentSkills,omitempty"`
	ConfirmedFacts     *[]ConfirmedFact     `json:"confirmedFacts,omitempty"`
	Experience         *[]Experience        `json:"experience,omitempty"`
	Languages          *[]Language          `json:"languages,omitempty"`
	Availability       *Availability        `json:"availability,omitempty"`
	CurrentLocation    *Location            `json:"currentLocation,omitempty"`
	WorkAuthorizations *[]WorkAuthorization `json:"workAuthorizations,omitempty"`
	Demographics       *Demographics        `json:"demographics,omitempty"`
}

type ProfileListItem struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type ProfileListResp struct {
	Profiles []ProfileListItem `json:"profiles"`
}

type InheritedSource struct {
	ProfileID   int64  `json:"profileId"`
	ProfileName string `json:"profileName"`
}

type ProfileDetailResp struct {
	ID        int64                      `json:"id"`
	Name      string                     `json:"name"`
	Active    bool                       `json:"active"`
	Own       ProfileData                `json:"own"`
	Effective ProfileData                `json:"effective"`
	Inherited map[string]InheritedSource `json:"inherited,omitempty"`
	Headline  string                     `json:"headline"`
}

type CreateProfileReq struct {
	Name string `json:"name"`
}

type ProfilePatchReq struct {
	Name               *string       `json:"name,omitempty"`
	Headline           *string       `json:"headline,omitempty"`
	TargetRoles        *[]string     `json:"targetRoles,omitempty"`
	Keywords           *[]string     `json:"keywords,omitempty"`
	AllOf              *[]string     `json:"allOf,omitempty"`
	TargetIndustries   *[]string     `json:"targetIndustries,omitempty"`
	Compensation       *Compensation `json:"compensation,omitempty"`
	Engagement         *[]string     `json:"engagement,omitempty"`
	Hours              *Hours        `json:"hours,omitempty"`
	WorkModes          *[]string     `json:"workModes,omitempty"`
	PreferredLocations *[]Location   `json:"preferredLocations,omitempty"`
	Relocation         *string       `json:"relocation,omitempty"`
	TimeZones          *[]string     `json:"timeZones,omitempty"`
	OtherRequirements  *string       `json:"otherRequirements,omitempty"`
}

type SetActiveReq struct {
	ID int64 `json:"id"`
}

type QAItem struct {
	ID        int64  `json:"id"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type QAListResp struct {
	Items []QAItem `json:"items"`
}

type QACreateReq struct {
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
}

type QAPatchReq struct {
	ID       int64   `json:"id"`
	Question *string `json:"question,omitempty"`
	Answer   *string `json:"answer,omitempty"`
}

type QADeleteReq struct {
	ID int64 `json:"id"`
}

func LoadMe(ctx context.Context, database *db.DB) (MeData, error) {
	var dataJSON string
	err := database.QueryRowContext(ctx, `SELECT data FROM me WHERE id=1`).Scan(&dataJSON)
	if err == sql.ErrNoRows {
		return MeData{}, nil
	}
	if err != nil {
		return MeData{}, fmt.Errorf("candidate: load me: %w", err)
	}
	var me MeData
	if err := json.Unmarshal([]byte(dataJSON), &me); err != nil {
		return MeData{}, fmt.Errorf("candidate: decode me: %w", err)
	}
	return me, nil
}

func saveMeData(ctx context.Context, database *db.DB, me MeData) error {
	NormalizeMeData(&me)
	data, err := json.Marshal(me)
	if err != nil {
		return fmt.Errorf("candidate: encode me: %w", err)
	}
	_, err = database.ExecContext(ctx, `INSERT OR REPLACE INTO me (id, data) VALUES (1, ?)`, string(data))
	if err != nil {
		return fmt.Errorf("candidate: save me: %w", err)
	}
	return nil
}

func activeProfileID(ctx context.Context, database *db.DB) (int64, error) {
	var v string
	err := database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='active_profile'`).Scan(&v)
	if err == nil {
		id, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr == nil && id > 0 {
			var check int64
			if chkErr := database.QueryRowContext(ctx, `SELECT id FROM profile WHERE id=?`, id).Scan(&check); chkErr == nil {
				return check, nil
			}
		}
	}
	var id int64
	if dbErr := database.QueryRowContext(ctx, `SELECT id FROM profile ORDER BY id LIMIT 1`).Scan(&id); dbErr != nil {
		if dbErr == sql.ErrNoRows {
			return 0, apperr.NotFound{Message: "Profile not found."}
		}
		return 0, fmt.Errorf("candidate: active profile: %w", dbErr)
	}
	return id, nil
}

func setActiveProfile(ctx context.Context, database *db.DB, id int64) error {
	_, err := database.ExecContext(ctx,
		`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile', ?)`,
		strconv.FormatInt(id, 10))
	if err != nil {
		return fmt.Errorf("candidate: set active profile: %w", err)
	}
	return nil
}

func loadProfileRow(ctx context.Context, database *db.DB, id int64) (ProfileData, error) {
	var dataJSON string
	err := database.QueryRowContext(ctx, `SELECT data FROM profile WHERE id=?`, id).Scan(&dataJSON)
	if err == sql.ErrNoRows {
		return ProfileData{}, apperr.NotFound{Message: "Profile not found."}
	}
	if err != nil {
		return ProfileData{}, fmt.Errorf("candidate: load profile %d: %w", id, err)
	}
	var pd ProfileData
	if err := json.Unmarshal([]byte(dataJSON), &pd); err != nil {
		return ProfileData{}, fmt.Errorf("candidate: decode profile %d: %w", id, err)
	}
	return pd, nil
}

func saveProfileRow(ctx context.Context, database *db.DB, id int64, pd ProfileData) error {
	NormalizeProfileData(&pd)
	data, err := json.Marshal(pd)
	if err != nil {
		return fmt.Errorf("candidate: encode profile %d: %w", id, err)
	}
	_, err = database.ExecContext(ctx, `UPDATE profile SET data=? WHERE id=?`, string(data), id)
	if err != nil {
		return fmt.Errorf("candidate: save profile %d: %w", id, err)
	}
	return nil
}

func firstProfileID(ctx context.Context, database *db.DB) (int64, error) {
	var id int64
	err := database.QueryRowContext(ctx, `SELECT id FROM profile ORDER BY id LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound{Message: "Profile not found."}
	}
	if err != nil {
		return 0, fmt.Errorf("candidate: first profile: %w", err)
	}
	return id, nil
}

func buildHeadline(pd ProfileData, me MeData) string {
	if strings.TrimSpace(pd.Headline) != "" {
		return pd.Headline
	}
	var exp *Experience
	for i := range me.Experience {
		if me.Experience[i].Current {
			exp = &me.Experience[i]
			break
		}
	}
	if exp == nil && len(me.Experience) > 0 {
		exp = &me.Experience[0]
	}
	var parts []string
	if exp != nil {
		if t := strings.TrimSpace(exp.Title); t != "" {
			parts = append(parts, t)
		}
		if c := strings.TrimSpace(exp.Company); c != "" {
			parts = append(parts, c)
		}
	}
	if me.CurrentLocation != nil {
		city := strings.TrimSpace(me.CurrentLocation.City)
		country := strings.TrimSpace(me.CurrentLocation.Country)
		if city != "" && country != "" {
			parts = append(parts, city+", "+country)
		} else if country != "" {
			parts = append(parts, country)
		}
	}
	if len(parts) == 0 {
		return "Candidate profile"
	}
	return strings.Join(parts, " · ")
}

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func clearInheritedUnchanged(patch *ProfilePatchReq, own, first ProfileData) {
	if patch.Compensation != nil && own.Compensation == nil && jsonEqual(patch.Compensation, first.Compensation) {
		patch.Compensation = nil
	}
	if patch.Engagement != nil && len(own.Engagement) == 0 && jsonEqual(*patch.Engagement, first.Engagement) {
		patch.Engagement = nil
	}
	if patch.Hours != nil && own.Hours == nil && jsonEqual(patch.Hours, first.Hours) {
		patch.Hours = nil
	}
	if patch.WorkModes != nil && len(own.WorkModes) == 0 && jsonEqual(*patch.WorkModes, first.WorkModes) {
		patch.WorkModes = nil
	}
	if patch.PreferredLocations != nil && len(own.PreferredLocations) == 0 && jsonEqual(*patch.PreferredLocations, first.PreferredLocations) {
		patch.PreferredLocations = nil
	}
	if patch.TargetIndustries != nil && len(own.TargetIndustries) == 0 && jsonEqual(*patch.TargetIndustries, first.TargetIndustries) {
		patch.TargetIndustries = nil
	}
}

func mergePatch[D any](dst D, src any) (D, error) {
	base, _ := json.Marshal(dst)
	over, _ := json.Marshal(src)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return dst, err
	}
	var p map[string]json.RawMessage
	json.Unmarshal(over, &p)
	for k, v := range p {
		m[k] = v
	}
	merged, _ := json.Marshal(m)
	var result D
	if err := json.Unmarshal(merged, &result); err != nil {
		return dst, fmt.Errorf("candidate: merge patch: %w", err)
	}
	trimStrings(&result)
	return result, nil
}

func trimStrings[T any](v *T) {
	rv := reflect.ValueOf(v).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if f := rv.Field(i); f.CanSet() && f.Kind() == reflect.String {
			f.SetString(strings.TrimSpace(f.String()))
		}
	}
}

func photoPath(filesDir string) string {
	return filepath.Join(filesDir, "me_photo")
}

func RegisterRoutes(mux *http.ServeMux, database *db.DB, filesDir string, pdfText TextSource, logger *slog.Logger, changed func(), enqueue func(kind, subject string) (int64, error)) {
	notify := func() {
		if changed != nil {
			changed()
		}
	}
	server.Handle(mux, "GET /api/me", logger, func(r *http.Request, _ struct{}) (MeData, error) {
		return LoadMe(r.Context(), database)
	})

	server.Handle(mux, "PATCH /api/me", logger, func(r *http.Request, patch MePatchReq) (MeData, error) {
		ctx := r.Context()
		me, err := LoadMe(ctx, database)
		if err != nil {
			return MeData{}, err
		}
		updated, err := mergePatch(me, patch)
		if err != nil {
			return MeData{}, err
		}
		if errs := ValidateMeData(updated); len(errs) > 0 {
			return MeData{}, apperr.Invalid{Message: strings.Join(errs, "; ")}
		}
		if err := saveMeData(ctx, database, updated); err != nil {
			return MeData{}, err
		}
		notify()
		return updated, nil
	})

	server.Handle(mux, "GET /api/profiles", logger, func(r *http.Request, _ struct{}) (ProfileListResp, error) {
		return listProfiles(r.Context(), database)
	})

	server.Handle(mux, "GET /api/profiles/{id}", logger, func(r *http.Request, _ struct{}) (ProfileDetailResp, error) {
		ctx := r.Context()
		id, err := parseProfileID(r)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		own, err := loadProfileRow(ctx, database, id)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		return loadProfileDetail(ctx, database, id, own)
	})

	server.Handle(mux, "POST /api/profiles", logger, func(r *http.Request, req CreateProfileReq) (ProfileDetailResp, error) {
		ctx := r.Context()
		name := strings.TrimSpace(req.Name)
		if name == "" {
			return ProfileDetailResp{}, apperr.Invalid{Message: "Name must not be empty."}
		}
		pd := ProfileData{Name: name}
		data, err := json.Marshal(pd)
		if err != nil {
			return ProfileDetailResp{}, fmt.Errorf("candidate: encode new profile: %w", err)
		}
		res, err := database.ExecContext(ctx, `INSERT INTO profile (data) VALUES (?)`, string(data))
		if err != nil {
			return ProfileDetailResp{}, fmt.Errorf("candidate: create profile: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return ProfileDetailResp{}, fmt.Errorf("candidate: new profile id: %w", err)
		}
		if err := setActiveProfile(ctx, database, newID); err != nil {
			return ProfileDetailResp{}, err
		}
		notify()
		return loadProfileDetail(ctx, database, newID, pd)
	})

	server.Handle(mux, "PATCH /api/profiles/{id}", logger, func(r *http.Request, patch ProfilePatchReq) (ProfileDetailResp, error) {
		ctx := r.Context()
		id, err := parseProfileID(r)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		own, err := loadProfileRow(ctx, database, id)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
			patch.Name = nil
		}
		firstID, _ := firstProfileID(ctx, database)
		if firstID != id {
			if firstPD, fErr := loadProfileRow(ctx, database, firstID); fErr == nil {
				clearInheritedUnchanged(&patch, own, firstPD)
			}
		}
		updated, err := mergePatch(own, patch)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		if errs := ValidateProfileData(updated); len(errs) > 0 {
			return ProfileDetailResp{}, apperr.Invalid{Message: strings.Join(errs, "; ")}
		}
		if err := saveProfileRow(ctx, database, id, updated); err != nil {
			return ProfileDetailResp{}, err
		}
		notify()
		return loadProfileDetail(ctx, database, id, updated)
	})

	server.Handle(mux, "DELETE /api/profiles/{id}", logger, func(r *http.Request, _ struct{}) (ProfileListResp, error) {
		ctx := r.Context()
		id, err := parseProfileID(r)
		if err != nil {
			return ProfileListResp{}, err
		}
		firstID, err := firstProfileID(ctx, database)
		if err != nil {
			return ProfileListResp{}, err
		}
		if id == firstID {
			return ProfileListResp{}, apperr.Invalid{Message: "The first profile cannot be deleted."}
		}
		if _, err := loadProfileRow(ctx, database, id); err != nil {
			return ProfileListResp{}, err
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return ProfileListResp{}, fmt.Errorf("candidate: delete profile tx: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DELETE FROM exclusion WHERE profile_id=?`, id); err != nil {
			return ProfileListResp{}, fmt.Errorf("candidate: delete profile exclusions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM decision WHERE profile=?`, id); err != nil {
			return ProfileListResp{}, fmt.Errorf("candidate: delete profile decisions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM profile WHERE id=?`, id); err != nil {
			return ProfileListResp{}, fmt.Errorf("candidate: delete profile: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return ProfileListResp{}, fmt.Errorf("candidate: delete profile commit: %w", err)
		}
		activeID, _ := activeProfileID(ctx, database)
		if activeID == 0 || activeID == id {
			setActiveProfile(ctx, database, firstID)
		}
		return listProfiles(ctx, database)
	})

	server.Handle(mux, "PUT /api/active-profile", logger, func(r *http.Request, req SetActiveReq) (ProfileListResp, error) {
		ctx := r.Context()
		if req.ID == 0 {
			return ProfileListResp{}, apperr.Invalid{Message: "ID is required."}
		}
		if _, err := loadProfileRow(ctx, database, req.ID); err != nil {
			return ProfileListResp{}, err
		}
		if err := setActiveProfile(ctx, database, req.ID); err != nil {
			return ProfileListResp{}, err
		}
		return listProfiles(ctx, database)
	})

	server.HandleRaw(mux, "PUT /api/me/photo", func(w http.ResponseWriter, r *http.Request) {
		if filesDir == "" {
			server.MapError(w, fmt.Errorf("candidate: no files dir configured"), logger)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			server.MapError(w, apperr.Invalid{Message: "Could not read the upload."}, logger)
			return
		}
		if len(body) == 0 {
			server.MapError(w, apperr.Invalid{Message: "The photo is empty."}, logger)
			return
		}
		ct := http.DetectContentType(body)
		if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
			server.MapError(w, apperr.Invalid{Message: "The photo must be a PNG, JPEG or WebP image."}, logger)
			return
		}
		if err := os.MkdirAll(filesDir, 0700); err != nil {
			server.MapError(w, fmt.Errorf("candidate: photo dir: %w", err), logger)
			return
		}
		if err := os.WriteFile(photoPath(filesDir), body, 0600); err != nil {
			server.MapError(w, fmt.Errorf("candidate: save photo: %w", err), logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	server.Handle(mux, "DELETE /api/me/photo", logger, func(r *http.Request, _ struct{}) (struct{}, error) {
		if filesDir == "" {
			return struct{}{}, fmt.Errorf("candidate: no files dir configured")
		}
		path := photoPath(filesDir)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return struct{}{}, fmt.Errorf("candidate: delete photo: %w", err)
		}
		return struct{}{}, nil
	})

	server.HandleRaw(mux, "GET /api/me/photo", func(w http.ResponseWriter, r *http.Request) {
		if filesDir == "" {
			server.MapError(w, apperr.NotFound{Message: "No photo set."}, logger)
			return
		}
		body, err := os.ReadFile(photoPath(filesDir))
		if os.IsNotExist(err) {
			server.MapError(w, apperr.NotFound{Message: "No photo set."}, logger)
			return
		}
		if err != nil {
			server.MapError(w, fmt.Errorf("candidate: read photo: %w", err), logger)
			return
		}
		ct := http.DetectContentType(body)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	server.Handle(mux, "GET /api/qa", logger, func(r *http.Request, _ struct{}) (QAListResp, error) {
		ctx := r.Context()
		rows, err := database.QueryContext(ctx,
			`SELECT id, question, answer, created_at, updated_at FROM qa ORDER BY id`)
		if err != nil {
			return QAListResp{}, fmt.Errorf("candidate: list qa: %w", err)
		}
		defer rows.Close()
		var items []QAItem
		for rows.Next() {
			var it QAItem
			if err := rows.Scan(&it.ID, &it.Question, &it.Answer, &it.CreatedAt, &it.UpdatedAt); err != nil {
				return QAListResp{}, fmt.Errorf("candidate: scan qa: %w", err)
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			return QAListResp{}, fmt.Errorf("candidate: list qa: %w", err)
		}
		if items == nil {
			items = []QAItem{}
		}
		return QAListResp{Items: items}, nil
	})

	server.Handle(mux, "POST /api/qa", logger, func(r *http.Request, req QACreateReq) (QAItem, error) {
		ctx := r.Context()
		q := strings.TrimSpace(req.Question)
		if q == "" {
			return QAItem{}, apperr.Invalid{Message: "Question must not be empty."}
		}
		a := strings.TrimSpace(req.Answer)
		now := time.Now().UTC().Format(time.RFC3339)
		res, err := database.ExecContext(ctx,
			`INSERT INTO qa (question, answer, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			q, a, now, now)
		if err != nil {
			return QAItem{}, fmt.Errorf("candidate: create qa: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return QAItem{}, fmt.Errorf("candidate: qa id: %w", err)
		}
		return QAItem{ID: id, Question: q, Answer: a, CreatedAt: now, UpdatedAt: now}, nil
	})

	server.Handle(mux, "PATCH /api/qa", logger, func(r *http.Request, req QAPatchReq) (QAItem, error) {
		ctx := r.Context()
		if req.ID == 0 {
			return QAItem{}, apperr.Invalid{Message: "ID is required."}
		}
		var current QAItem
		err := database.QueryRowContext(ctx,
			`SELECT id, question, answer, created_at, updated_at FROM qa WHERE id=?`, req.ID).
			Scan(&current.ID, &current.Question, &current.Answer, &current.CreatedAt, &current.UpdatedAt)
		if err == sql.ErrNoRows {
			return QAItem{}, apperr.NotFound{Message: "Q&A entry not found."}
		}
		if err != nil {
			return QAItem{}, fmt.Errorf("candidate: load qa: %w", err)
		}
		if req.Question != nil {
			current.Question = strings.TrimSpace(*req.Question)
		}
		if req.Answer != nil {
			current.Answer = strings.TrimSpace(*req.Answer)
		}
		if strings.TrimSpace(current.Question) == "" {
			return QAItem{}, apperr.Invalid{Message: "Question must not be empty."}
		}
		now := time.Now().UTC().Format(time.RFC3339)
		current.UpdatedAt = now
		_, err = database.ExecContext(ctx,
			`UPDATE qa SET question=?, answer=?, updated_at=? WHERE id=?`,
			current.Question, current.Answer, now, current.ID)
		if err != nil {
			return QAItem{}, fmt.Errorf("candidate: update qa: %w", err)
		}
		return current, nil
	})

	server.Handle(mux, "DELETE /api/qa", logger, func(r *http.Request, req QADeleteReq) (struct{}, error) {
		ctx := r.Context()
		if req.ID == 0 {
			return struct{}{}, apperr.Invalid{Message: "ID is required."}
		}
		res, err := database.ExecContext(ctx, `DELETE FROM qa WHERE id=?`, req.ID)
		if err != nil {
			return struct{}{}, fmt.Errorf("candidate: delete qa: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return struct{}{}, apperr.NotFound{Message: "Q&A entry not found."}
		}
		return struct{}{}, nil
	})

	server.Handle(mux, "GET /api/reference", logger, func(r *http.Request, _ struct{}) (ReferenceResp, error) {
		return referenceData, nil
	})

	server.HandleRaw(mux, "POST /api/documents", func(w http.ResponseWriter, r *http.Request) {
		if filesDir == "" {
			server.MapError(w, fmt.Errorf("candidate: no files dir configured"), logger)
			return
		}
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/json") {
			var req struct {
				Name string `json:"name"`
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				server.MapError(w, apperr.Invalid{Message: "Invalid request body."}, logger)
				return
			}
			req.Name = strings.TrimSpace(req.Name)
			req.Text = strings.TrimSpace(req.Text)
			if req.Name == "" {
				server.MapError(w, apperr.Invalid{Message: "Name must not be empty."}, logger)
				return
			}
			item, err := saveDocument(r.Context(), database, filesDir, req.Name, "text", nil, req.Text, "ready", "")
			if server.MapError(w, err, logger) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(item)
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			server.MapError(w, apperr.Invalid{Message: "Name must not be empty."}, logger)
			return
		}
		kind, ok := kindFromName(name)
		if !ok {
			server.MapError(w, apperr.Invalid{Message: "The file must be a PDF, Word document, Markdown or plain text file."}, logger)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			server.MapError(w, apperr.Invalid{Message: "Could not read the upload."}, logger)
			return
		}
		if len(body) == 0 {
			server.MapError(w, apperr.Invalid{Message: "The file is empty."}, logger)
			return
		}
		if err := checkDocumentBytes(kind, body); err != nil {
			server.MapError(w, err, logger)
			return
		}
		text, state, stateReason := extractText(r.Context(), pdfText, logger, kind, body)
		item, err := saveDocument(r.Context(), database, filesDir, name, kind, body, text, state, stateReason)
		if server.MapError(w, err, logger) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(item)
	})

	server.Handle(mux, "GET /api/documents", logger, func(r *http.Request, _ struct{}) (DocumentListResp, error) {
		return listDocumentsFromDB(r.Context(), database)
	})

	server.Handle(mux, "DELETE /api/documents/{id}", logger, func(r *http.Request, _ struct{}) (struct{}, error) {
		id, err := parseDocumentID(r)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, deleteDocument(r.Context(), database, filesDir, id)
	})

	server.Handle(mux, "PATCH /api/documents/{id}", logger, func(r *http.Request, req DocumentPatchReq) (DocumentItem, error) {
		id, err := parseDocumentID(r)
		if err != nil {
			return DocumentItem{}, err
		}
		if req.Evidence == nil {
			return DocumentItem{}, apperr.Invalid{Message: "Evidence field is required."}
		}
		return setDocumentEvidence(r.Context(), database, id, *req.Evidence)
	})

	server.HandleRaw(mux, "GET /api/documents/{id}/text", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseDocumentID(r)
		if err != nil {
			server.MapError(w, err, logger)
			return
		}
		row, err := loadDocumentRow(r.Context(), database, id)
		if server.MapError(w, err, logger) {
			return
		}
		if row.State != "ready" {
			server.MapError(w, apperr.NotFound{Message: "No text available for this document."}, logger)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(row.Text))
	})

	server.HandleRaw(mux, "GET /api/documents/{id}/original", func(w http.ResponseWriter, r *http.Request) {
		if filesDir == "" {
			server.MapError(w, apperr.NotFound{Message: "No original file for this document."}, logger)
			return
		}
		id, err := parseDocumentID(r)
		if err != nil {
			server.MapError(w, err, logger)
			return
		}
		row, err := loadDocumentRow(r.Context(), database, id)
		if server.MapError(w, err, logger) {
			return
		}
		if row.FileName == "" {
			server.MapError(w, apperr.NotFound{Message: "No original file for this document."}, logger)
			return
		}
		body, err := os.ReadFile(filepath.Join(filesDir, row.FileName))
		if os.IsNotExist(err) {
			server.MapError(w, apperr.NotFound{Message: "No original file for this document."}, logger)
			return
		}
		if err != nil {
			server.MapError(w, fmt.Errorf("candidate: read document file: %w", err), logger)
			return
		}
		ct := http.DetectContentType(body)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	server.Handle(mux, "POST /api/documents/{id}/refresh", logger, func(r *http.Request, _ struct{}) (DocumentItem, error) {
		ctx := r.Context()
		id, err := parseDocumentID(r)
		if err != nil {
			return DocumentItem{}, err
		}
		row, err := loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		if row.Kind != "pdf" || strings.TrimSpace(row.Text) != "" {
			return rowToItem(row), nil
		}
		if row.FileName == "" || filesDir == "" {
			return DocumentItem{}, apperr.NotFound{Message: "No original file for this document."}
		}
		body, err := os.ReadFile(filepath.Join(filesDir, row.FileName))
		if err != nil {
			return DocumentItem{}, apperr.NotFound{Message: "No original file for this document."}
		}
		text, state, stateReason := extractText(ctx, pdfText, logger, row.Kind, body)
		if err := updateDocumentText(ctx, database, id, text, state, stateReason); err != nil {
			return DocumentItem{}, err
		}
		row, err = loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		return rowToItem(row), nil
	})

	server.Handle(mux, "POST /api/documents/{id}/analyze", logger, func(r *http.Request, _ struct{}) (DocumentItem, error) {
		ctx := r.Context()
		id, err := parseDocumentID(r)
		if err != nil {
			return DocumentItem{}, err
		}
		row, err := loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		if row.State == "unavailable" || strings.TrimSpace(row.Text) == "" {
			return DocumentItem{}, apperr.Invalid{Message: "This document does not have text available for analysis."}
		}
		if err := updateDocumentAnalysing(ctx, database, id); err != nil {
			return DocumentItem{}, fmt.Errorf("candidate: set analysing: %w", err)
		}
		if enqueue != nil {
			enqueue("analyze_document", strconv.FormatInt(id, 10))
		}
		row, err = loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		return rowToItem(row), nil
	})

	server.Handle(mux, "GET /api/proposals", logger, func(r *http.Request, _ struct{}) (ProposalListResp, error) {
		return listProposals(r.Context(), database)
	})
}

func listProfiles(ctx context.Context, database *db.DB) (ProfileListResp, error) {
	activeID, _ := activeProfileID(ctx, database)
	rows, err := database.QueryContext(ctx, `SELECT id, data FROM profile ORDER BY id`)
	if err != nil {
		return ProfileListResp{}, fmt.Errorf("candidate: list profiles: %w", err)
	}
	defer rows.Close()
	var items []ProfileListItem
	for rows.Next() {
		var id int64
		var dataJSON string
		if err := rows.Scan(&id, &dataJSON); err != nil {
			return ProfileListResp{}, err
		}
		var pd ProfileData
		json.Unmarshal([]byte(dataJSON), &pd)
		items = append(items, ProfileListItem{ID: id, Name: pd.Name, Active: id == activeID})
	}
	if err := rows.Err(); err != nil {
		return ProfileListResp{}, fmt.Errorf("candidate: list profiles: %w", err)
	}
	if items == nil {
		items = []ProfileListItem{}
	}
	return ProfileListResp{Profiles: items}, nil
}

func loadProfileDetail(ctx context.Context, database *db.DB, id int64, own ProfileData) (ProfileDetailResp, error) {
	firstID, err := firstProfileID(ctx, database)
	if err != nil {
		return ProfileDetailResp{}, err
	}
	firstPD, err := loadProfileRow(ctx, database, firstID)
	if err != nil {
		return ProfileDetailResp{}, err
	}
	activeID, _ := activeProfileID(ctx, database)
	me, _ := LoadMe(ctx, database)
	effective := own
	var inherited map[string]InheritedSource
	if id != firstID {
		effective = Effective(own, firstPD)
		for _, f := range InheritedFields(own, firstPD) {
			if inherited == nil {
				inherited = make(map[string]InheritedSource)
			}
			inherited[f] = InheritedSource{ProfileID: firstID, ProfileName: firstPD.Name}
		}
	}
	return ProfileDetailResp{
		ID:        id,
		Name:      own.Name,
		Active:    id == activeID,
		Own:       own,
		Effective: effective,
		Inherited: inherited,
		Headline:  buildHeadline(own, me),
	}, nil
}

func parseProfileID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, apperr.NotFound{Message: "Profile not found."}
	}
	return id, nil
}

func parseDocumentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, apperr.NotFound{Message: "Document not found."}
	}
	return id, nil
}

func ActiveProfileID(ctx context.Context, database *db.DB) (int64, error) {
	return activeProfileID(ctx, database)
}

func SaveMe(ctx context.Context, database *db.DB, me MeData) error {
	return saveMeData(ctx, database, me)
}

func EffectiveProfile(ctx context.Context, database *db.DB, profileID int64) (ProfileData, error) {
	pd, err := loadProfileRow(ctx, database, profileID)
	if err != nil {
		return ProfileData{}, err
	}
	firstID, err := firstProfileID(ctx, database)
	if err != nil || firstID == profileID {
		return pd, err
	}
	base, err := loadProfileRow(ctx, database, firstID)
	if err != nil {
		return ProfileData{}, err
	}
	return Effective(pd, base), nil
}

func ProfileSearch(ctx context.Context, database *db.DB, profileID int64) (Profile, error) {
	pd, err := EffectiveProfile(ctx, database, profileID)
	if err != nil {
		return Profile{}, nil
	}

	industries := pd.TargetIndustries
	if len(industries) > 0 {
		allowed := make(map[string]bool, len(industry.Names()))
		for _, n := range industry.Names() {
			allowed[n] = true
		}
		filtered := industries[:0:0]
		for _, n := range industries {
			if allowed[n] {
				filtered = append(filtered, n)
			}
		}
		industries = filtered
	}

	return Profile{
		TargetRoles:      pd.TargetRoles,
		Keywords:         pd.Keywords,
		AllOf:            pd.AllOf,
		TargetIndustries: industries,
	}, nil
}
