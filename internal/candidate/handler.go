package candidate

import (
	"context"
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

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
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

	named map[string]json.RawMessage `json:"-"`
}

func (p *MePatchReq) UnmarshalJSON(data []byte) error {
	type plain MePatchReq
	if err := json.Unmarshal(data, (*plain)(p)); err != nil {
		return err
	}
	return json.Unmarshal(data, &p.named)
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
	Name              *string       `json:"name,omitempty"`
	Headline          *string       `json:"headline,omitempty"`
	TargetRoles       *[]string     `json:"targetRoles,omitempty"`
	Keywords          *[]string     `json:"keywords,omitempty"`
	AllOf             *[]string     `json:"allOf,omitempty"`
	Compensation      *Compensation `json:"compensation,omitempty"`
	EngagementType    *Buckets      `json:"engagementType,omitempty"`
	WorkingTime       *Buckets      `json:"workingTime,omitempty"`
	Hours             *Hours        `json:"hours,omitempty"`
	WorkMode          *Buckets      `json:"workMode,omitempty"`
	Domain            *Buckets      `json:"domain,omitempty"`
	Locations         *PlaceBuckets `json:"locations,omitempty"`
	Relocation        *string       `json:"relocation,omitempty"`
	TimeZones         *[]string     `json:"timeZones,omitempty"`
	OtherRequirements *string       `json:"otherRequirements,omitempty"`

	named map[string]json.RawMessage `json:"-"`
}

func (p *ProfilePatchReq) UnmarshalJSON(data []byte) error {
	type plain ProfilePatchReq
	if err := json.Unmarshal(data, (*plain)(p)); err != nil {
		return err
	}
	return json.Unmarshal(data, &p.named)
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
		delete(patch.named, "compensation")
	}
	if patch.Hours != nil && own.Hours == nil && jsonEqual(patch.Hours, first.Hours) {
		delete(patch.named, "hours")
	}
	for _, g := range []struct {
		name         string
		sent, first  any
		inheritsHere bool
	}{
		{"engagementType", patch.EngagementType, first.EngagementType, own.EngagementType.IsZero()},
		{"workingTime", patch.WorkingTime, first.WorkingTime, own.WorkingTime.IsZero()},
		{"workMode", patch.WorkMode, first.WorkMode, own.WorkMode.IsZero()},
		{"locations", patch.Locations, first.Locations, own.Locations.IsZero()},
		{"domain", patch.Domain, first.Domain, own.Domain.IsZero()},
	} {
		if _, sent := patch.named[g.name]; sent && g.inheritsHere && jsonEqual(g.sent, g.first) {
			delete(patch.named, g.name)
		}
	}
}

func mergePatch[D any](dst D, named map[string]json.RawMessage) (D, error) {
	base, err := json.Marshal(dst)
	if err != nil {
		return dst, fmt.Errorf("candidate: merge patch: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return dst, fmt.Errorf("candidate: merge patch: %w", err)
	}
	for k, v := range named {
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return dst, fmt.Errorf("candidate: merge patch: %w", err)
	}
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

const photoFileName = "me_photo"

func photoPath(filesDir string) string {
	return filepath.Join(filesDir, photoFileName)
}

func serveStoredFile(w http.ResponseWriter, r *http.Request, logger *slog.Logger, filesDir, missing string, storedName func(*http.Request) (string, error)) {
	if filesDir == "" {
		server.MapError(w, apperr.NotFound{Message: missing}, logger)
		return
	}
	name, err := storedName(r)
	if err != nil {
		server.MapError(w, err, logger)
		return
	}
	if name == "" {
		server.MapError(w, apperr.NotFound{Message: missing}, logger)
		return
	}
	body, err := os.ReadFile(filepath.Join(filesDir, name))
	if os.IsNotExist(err) {
		server.MapError(w, apperr.NotFound{Message: missing}, logger)
		return
	}
	if err != nil {
		server.MapError(w, fmt.Errorf("candidate: read stored file: %w", err), logger)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(body))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func RegisterRoutes(mux *http.ServeMux, database *db.DB, filesDir string, readers Readers, logger *slog.Logger, changed func(), enqueue func(kind, subject string) (int64, error)) {
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
		updated, err := mergePatch(me, patch.named)
		if err != nil {
			return MeData{}, err
		}
		if errs := ValidateMeData(updated); len(errs) > 0 {
			return MeData{}, apperr.Invalid{Message: strings.Join(errs, "; ")}
		}
		if err := SaveMe(ctx, database, updated); err != nil {
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
		own, err := LoadProfileData(ctx, database, id)
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
		newID, pd, err := createProfile(ctx, database, name)
		if err != nil {
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
		own, err := LoadProfileData(ctx, database, id)
		if err != nil {
			return ProfileDetailResp{}, err
		}
		if patch.Name == nil || strings.TrimSpace(*patch.Name) == "" {
			delete(patch.named, "name")
		}
		firstID, _ := firstProfileID(ctx, database)
		if firstID != id {
			if firstPD, fErr := LoadProfileData(ctx, database, firstID); fErr == nil {
				clearInheritedUnchanged(&patch, own, firstPD)
			}
		}
		updated, err := mergePatch(own, patch.named)
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
		if err := deleteProfile(ctx, database, id); err != nil {
			return ProfileListResp{}, err
		}
		return listProfiles(ctx, database)
	})

	server.Handle(mux, "PUT /api/active-profile", logger, func(r *http.Request, req SetActiveReq) (ProfileListResp, error) {
		ctx := r.Context()
		if req.ID == 0 {
			return ProfileListResp{}, apperr.Invalid{Message: "ID is required."}
		}
		if _, err := LoadProfileData(ctx, database, req.ID); err != nil {
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
		serveStoredFile(w, r, logger, filesDir, "No photo set.", func(*http.Request) (string, error) {
			return photoFileName, nil
		})
	})

	server.Handle(mux, "GET /api/qa", logger, func(r *http.Request, _ struct{}) (QAListResp, error) {
		return listQA(r.Context(), database)
	})

	server.Handle(mux, "POST /api/qa", logger, func(r *http.Request, req QACreateReq) (QAItem, error) {
		return createQA(r.Context(), database, req)
	})

	server.Handle(mux, "PATCH /api/qa", logger, func(r *http.Request, req QAPatchReq) (QAItem, error) {
		return patchQA(r.Context(), database, req)
	})

	server.Handle(mux, "DELETE /api/qa", logger, func(r *http.Request, req QADeleteReq) (struct{}, error) {
		return struct{}{}, removeQA(r.Context(), database, req.ID)
	})

	server.Handle(mux, "GET /api/reference", logger, func(r *http.Request, _ struct{}) (ReferenceResp, error) {
		return referenceData, nil
	})

	server.HandleRaw(mux, "POST /api/documents", func(w http.ResponseWriter, r *http.Request) {
		if filesDir == "" {
			server.MapError(w, fmt.Errorf("candidate: no files dir configured"), logger)
			return
		}
		var k documentKind
		var name string
		var source []byte
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				server.MapError(w, apperr.Invalid{Message: "Invalid request body."}, logger)
				return
			}
			var text string
			var ok bool
			k, text, ok = kindFromBody(body)
			if !ok {
				server.MapError(w, apperr.Invalid{Message: "Send the text of the document."}, logger)
				return
			}
			name, _ = body["name"].(string)
			name, source = strings.TrimSpace(name), []byte(strings.TrimSpace(text))
		} else {
			name = strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				server.MapError(w, apperr.Invalid{Message: "Name must not be empty."}, logger)
				return
			}
			var ok bool
			if k, ok = kindFromName(name); !ok {
				server.MapError(w, apperr.Invalid{Message: notADocumentKind}, logger)
				return
			}
			var err error
			if source, err = io.ReadAll(r.Body); err != nil {
				server.MapError(w, apperr.Invalid{Message: "Could not read the upload."}, logger)
				return
			}
			if len(source) == 0 {
				server.MapError(w, apperr.Invalid{Message: "The file is empty."}, logger)
				return
			}
		}
		item, err := addDocument(r.Context(), database, filesDir, readers, logger, k, name, source)
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
		serveStoredFile(w, r, logger, filesDir, "No original file for this document.", func(r *http.Request) (string, error) {
			id, err := parseDocumentID(r)
			if err != nil {
				return "", err
			}
			row, err := loadDocumentRow(r.Context(), database, id)
			if err != nil {
				return "", err
			}
			return row.FileName, nil
		})
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
		return refreshDocument(ctx, database, filesDir, readers, logger, row)
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
		if err := updateDocumentState(ctx, database, id, "analysing", ""); err != nil {
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

	server.Handle(mux, "POST /api/proposals/{id}/accept", logger, AcceptProposalHandler(database, changed))
	server.Handle(mux, "POST /api/proposals/{id}/reject", logger, RejectProposalHandler(database))
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

func EffectiveProfile(ctx context.Context, database *db.DB, profileID int64) (ProfileData, error) {
	pd, err := LoadProfileData(ctx, database, profileID)
	if err != nil {
		return ProfileData{}, err
	}
	firstID, err := firstProfileID(ctx, database)
	if err != nil || firstID == profileID {
		return pd, err
	}
	base, err := LoadProfileData(ctx, database, firstID)
	if err != nil {
		return ProfileData{}, err
	}
	return Effective(pd, base), nil
}

func ProfileSearch(ctx context.Context, database *db.DB, profileID int64) (Profile, error) {
	pd, err := EffectiveProfile(ctx, database, profileID)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		TargetRoles: pd.TargetRoles,
		Keywords:    pd.Keywords,
		AllOf:       pd.AllOf,
	}, nil
}
