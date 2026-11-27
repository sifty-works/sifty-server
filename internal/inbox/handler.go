package inbox

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/server"
)

type ListReq struct {
	Stage      Stage  `json:"stage"`
	Profile    int64  `json:"profile"`
	Q          string `json:"q"`
	Published  string `json:"published,omitempty"`
	Remote     bool   `json:"remote,omitempty"`
	Leadership bool   `json:"leadership,omitempty"`
	Sources    string `json:"sources,omitempty"`
	Unread     bool   `json:"unread,omitempty"`
	Fit        string `json:"fit,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Limit      int64  `json:"limit,omitempty"`
	Offset     int64  `json:"offset,omitempty"`
}

type ListResp struct {
	Rows         []Row         `json:"rows"`
	Counts       Counts        `json:"counts"`
	SourceCounts []SourceCount `json:"sourceCounts"`
	Total        int           `json:"total"`
	Matching     int           `json:"matching"`
	Empty        string        `json:"empty"`
	Profile      int64         `json:"profile"`
}

type VacancyViewReq struct {
	Profile int64 `json:"profile"`
}

type DecisionReq struct {
	Profile   int64  `json:"profile"`
	From      Stage  `json:"from"`
	To        Stage  `json:"to"`
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}

type DecisionResp struct {
	Row    Row    `json:"row"`
	Counts Counts `json:"counts"`
}

type DeleteVacancyResp struct {
	Counts Counts `json:"counts"`
}

type AddExclusionReq struct {
	Kind    ExclusionKind `json:"kind"`
	Value   string        `json:"value"`
	Profile int64         `json:"profile,omitempty"`
}

type DeleteExclusionReq struct {
	ID      int64 `json:"id"`
	Profile int64 `json:"profile,omitempty"`
}

type ExclusionListReq struct {
	Profile int64 `json:"profile,omitempty"`
}

type PreviewReq struct {
	Kind  ExclusionKind `json:"kind"`
	Value string        `json:"value"`
}

type PreviewResp struct {
	Text string `json:"text"`
}

type SkillsDecisionReq struct {
	VacancyID int64  `json:"vacancyId"`
	Skill     string `json:"skill,omitempty"`
	Level     string `json:"level,omitempty"`
	ReqIndex  *int   `json:"requirementIndex,omitempty"`
	Have      *bool  `json:"have,omitempty"`
}

type SkillsDecisionResp struct {
	Notice      string      `json:"notice"`
	VacancyView VacancyView `json:"vacancyView"`
}

func RegisterRoutes(mux *http.ServeMux, database *db.DB, now func() time.Time, logger *slog.Logger, collectActive func() bool, getSourceNames func() map[string]string, rules func() fit.Rules, changed func(), enqueue func(kind, subject string) (int64, error), opened func(ctx context.Context, vacancyID int64) error) {
	cat := catalog.New(database, now)
	if rules == nil {
		rules = func() fit.Rules { d := config.Defaults(); return fit.Rules{Weights: d.Weights, Rates: d.Rates} }
	}
	sourceNamesFor := func() map[string]string {
		if getSourceNames == nil {
			return nil
		}
		return getSourceNames()
	}

	server.Handle(mux, "POST /api/me/skills", logger, func(req *http.Request, body SkillsDecisionReq) (SkillsDecisionResp, error) {
		ctx := req.Context()
		if body.VacancyID <= 0 {
			return SkillsDecisionResp{}, apperr.Invalid{Message: "vacancyId is required."}
		}
		vacancyID, err := resolveVacancy(ctx, database, body.VacancyID)
		if err != nil {
			return SkillsDecisionResp{}, err
		}
		pid, err := resolveProfile(ctx, database, 0)
		if err != nil {
			return SkillsDecisionResp{}, err
		}
		res, err := applySkillsDecision(ctx, database, vacancyID, pid, body, rules())
		if err != nil {
			return SkillsDecisionResp{}, err
		}
		if changed != nil && res.added {
			changed()
		}
		view, err := vacancyView(ctx, database, now, nil, vacancyID, pid, sourceNamesFor(), rules())
		if err != nil {
			return SkillsDecisionResp{}, err
		}
		return SkillsDecisionResp{Notice: res.notice, VacancyView: view}, nil
	})

	server.Handle(mux, "GET /api/vacancies", logger, func(r *http.Request, req ListReq) (ListResp, error) {
		stage := strings.TrimSpace(string(req.Stage))
		if stage == "" {
			stage = "inbox"
		}
		switch stage {
		case "inbox", "shortlist", "applied", "dismissed":
		default:
			return ListResp{}, apperr.Invalid{Message: "Invalid stage."}
		}
		limit, offset, err := parseWindow(r, req)
		if err != nil {
			return ListResp{}, err
		}
		pid, err := resolveProfile(r.Context(), database, req.Profile)
		if err != nil {
			return ListResp{}, err
		}
		sources := splitComma(req.Sources)
		published := 0
		switch req.Published {
		case "7":
			published = 7
		case "30":
			published = 30
		}
		unread := req.Unread && stage != "applied" && stage != "dismissed"
		f := Filters{
			Published:  published,
			Remote:     req.Remote,
			Leadership: req.Leadership,
			Sources:    sources,
			Unread:     unread,
			Fit:        splitComma(req.Fit),
		}
		rows, counts, srcCounts, total, matching, err := list(r.Context(), database, now, pid, stage, strings.TrimSpace(req.Q), req.Sort, f, limit, offset, rules())
		if err != nil {
			return ListResp{}, err
		}
		if srcCounts == nil {
			srcCounts = []SourceCount{}
		}
		hasFilters := req.Q != "" || len(sources) > 0 ||
			published != 0 || req.Remote || req.Leadership || unread || len(f.Fit) > 0
		return ListResp{
			Rows:         rows,
			Counts:       counts,
			SourceCounts: srcCounts,
			Total:        total,
			Matching:     matching,
			Empty:        emptyText(matching, stage, hasFilters),
			Profile:      pid,
		}, nil
	})

	server.Handle(mux, "GET /api/vacancies/{id}", logger, func(r *http.Request, req VacancyViewReq) (VacancyView, error) {
		pid, err := resolveProfile(r.Context(), database, req.Profile)
		if err != nil {
			return VacancyView{}, err
		}
		id, err := parseID(r)
		if err != nil {
			return VacancyView{}, err
		}
		vacancyID, err := resolveVacancy(r.Context(), database, id)
		if err != nil {
			return VacancyView{}, err
		}
		if opened != nil {
			if err := opened(r.Context(), vacancyID); err != nil {
				logger.Warn("reading of an opened vacancy not queued", "vacancy", vacancyID, "err", err)
			}
		}
		return vacancyView(r.Context(), database, now, enqueue, vacancyID, pid, sourceNamesFor(), rules())
	})

	server.Handle(mux, "POST /api/vacancies/{id}/shortlist-check/retry", logger, func(r *http.Request, req CheckRetryReq) (VacancyView, error) {
		vacancyID, pid, err := resolveRequestIDs(r, database, req.Profile)
		if err != nil {
			return VacancyView{}, err
		}
		if err := retryCheck(r.Context(), database, enqueue, pid, vacancyID); err != nil {
			return VacancyView{}, err
		}
		return vacancyView(r.Context(), database, now, nil, vacancyID, pid, sourceNamesFor(), rules())
	})

	server.Handle(mux, "POST /api/vacancies/{id}/decision", logger, func(r *http.Request, req DecisionReq) (DecisionResp, error) {
		vacancyID, pid, err := resolveRequestIDs(r, database, req.Profile)
		if err != nil {
			return DecisionResp{}, err
		}
		row, counts, err := applyDecision(r.Context(), database, now, enqueue, vacancyID, pid, string(req.From), string(req.To), req.Reason, req.Confirmed, rules())
		if err != nil {
			return DecisionResp{}, err
		}
		return DecisionResp{Row: row, Counts: counts}, nil
	})

	server.Handle(mux, "POST /api/vacancies/{id}/seen", logger, func(r *http.Request, _ struct{}) (struct{}, error) {
		id, err := parseID(r)
		if err != nil {
			return struct{}{}, err
		}
		vacancyID, err := resolveVacancy(r.Context(), database, id)
		if err != nil {
			return struct{}{}, err
		}
		_, err = database.ExecContext(r.Context(),
			`UPDATE vacancy SET seen_at=? WHERE id=? AND seen_at=''`,
			time.Now().UTC().Format(time.RFC3339), vacancyID)
		return struct{}{}, err
	})

	server.Handle(mux, "DELETE /api/vacancies/{id}", logger, func(r *http.Request, _ struct{}) (DeleteVacancyResp, error) {
		profileID, err := parseProfileQuery(r)
		if err != nil {
			return DeleteVacancyResp{}, err
		}
		vacancyID, pid, err := resolveRequestIDs(r, database, profileID)
		if err != nil {
			return DeleteVacancyResp{}, err
		}
		var hasDecision int
		database.QueryRowContext(r.Context(),
			`SELECT count(*) FROM decision WHERE vacancy=? AND profile=?`, vacancyID, pid).Scan(&hasDecision)
		if hasDecision == 0 {
			return DeleteVacancyResp{}, apperr.Conflict{Message: "Dismiss roles in Inbox; permanent Delete is available outside Inbox"}
		}
		if collectActive != nil && collectActive() {
			return DeleteVacancyResp{}, apperr.Busy{Message: "Collection is running; try again when it ends."}
		}
		if _, err := fit.LoadProfile(r.Context(), database, pid, rules()); err != nil {
			return DeleteVacancyResp{}, err
		}
		if err := cat.Delete(r.Context(), vacancyID); err != nil {
			return DeleteVacancyResp{}, err
		}
		counts, err := loadCounts(r.Context(), database, now(), pid)
		if err != nil {
			return DeleteVacancyResp{}, err
		}
		return DeleteVacancyResp{Counts: counts}, nil
	})

	server.Handle(mux, "GET /api/exclusions", logger, func(r *http.Request, req ExclusionListReq) (ExclusionsResp, error) {
		rows, err := queryExclusions(r.Context(), database, req.Profile)
		if err != nil {
			return ExclusionsResp{}, err
		}
		return toExclusionsResp(rows, req.Profile), nil
	})

	server.Handle(mux, "POST /api/exclusions", logger, func(r *http.Request, req AddExclusionReq) (ExclusionsResp, error) {
		rows, err := addExclusion(r.Context(), database, string(req.Kind), req.Value, req.Profile)
		if err != nil {
			return ExclusionsResp{}, err
		}
		return toExclusionsResp(rows, req.Profile), nil
	})

	server.Handle(mux, "DELETE /api/exclusions", logger, func(r *http.Request, req DeleteExclusionReq) (ExclusionsResp, error) {
		rows, err := removeExclusion(r.Context(), database, req.ID, req.Profile)
		if err != nil {
			return ExclusionsResp{}, err
		}
		return toExclusionsResp(rows, req.Profile), nil
	})

	server.Handle(mux, "GET /api/exclusions/preview", logger, func(r *http.Request, req PreviewReq) (PreviewResp, error) {
		n, err := previewCount(r.Context(), database, string(req.Kind), req.Value)
		if err != nil {
			return PreviewResp{}, err
		}
		text := "Would hide 0 roles"
		if n == 1 {
			text = "Would hide 1 role"
		} else if n > 1 {
			text = fmt.Sprintf("Would hide %d roles", n)
		}
		return PreviewResp{Text: text}, nil
	})
}

const (
	defaultListLimit = 100
	maxListLimit     = 200
)

func parseWindow(r *http.Request, req ListReq) (int, int, error) {
	q := r.URL.Query()
	limit := int64(defaultListLimit)
	if q.Has("limit") {
		if req.Limit < 1 || req.Limit > maxListLimit {
			return 0, 0, apperr.Invalid{Message: "The limit is a whole number from 1 to 200."}
		}
		limit = req.Limit
	}
	if q.Has("offset") && (q.Get("offset") == "" || req.Offset < 0) {
		return 0, 0, apperr.Invalid{Message: "The offset is a whole number from 0."}
	}
	return int(limit), int(req.Offset), nil
}

func parseID(req *http.Request) (int64, error) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		return 0, apperr.NotFound{Message: "Role not found."}
	}
	return id, nil
}

func parseProfileQuery(req *http.Request) (int64, error) {
	p := req.URL.Query().Get("profile")
	if p == "" {
		return 0, nil
	}
	pid, err := strconv.ParseInt(p, 10, 64)
	if err != nil {
		return 0, apperr.Invalid{Message: "Invalid profile."}
	}
	return pid, nil
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func resolveRequestIDs(req *http.Request, database *db.DB, profileID int64) (int64, int64, error) {
	id, err := parseID(req)
	if err != nil {
		return 0, 0, err
	}
	vacancyID, err := resolveVacancy(req.Context(), database, id)
	if err != nil {
		return 0, 0, err
	}
	pid, err := resolveProfile(req.Context(), database, profileID)
	if err != nil {
		return 0, 0, err
	}
	return vacancyID, pid, nil
}
