package candidate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
)

type ProfileData struct {
	Name              string        `json:"name"`
	Headline          string        `json:"headline,omitempty"`
	TargetRoles       []string      `json:"targetRoles,omitempty"`
	Keywords          []string      `json:"keywords,omitempty"`
	AllOf             []string      `json:"allOf,omitempty"`
	Compensation      *Compensation `json:"compensation,omitempty"`
	EngagementType    Buckets       `json:"engagementType"`
	WorkingTime       Buckets       `json:"workingTime"`
	Hours             *Hours        `json:"hours,omitempty"`
	WorkMode          Buckets       `json:"workMode"`
	Locations         PlaceBuckets  `json:"locations"`
	Relocation        string        `json:"relocation,omitempty"`
	TimeZones         []string      `json:"timeZones,omitempty"`
	OtherRequirements string        `json:"otherRequirements,omitempty"`
}

func Effective(p, base ProfileData) ProfileData {
	out := p
	if out.Compensation == nil {
		out.Compensation = base.Compensation
	}
	if out.EngagementType.IsZero() {
		out.EngagementType = base.EngagementType
	}
	if out.WorkingTime.IsZero() {
		out.WorkingTime = base.WorkingTime
	}
	if out.Hours == nil {
		out.Hours = base.Hours
	}
	if out.WorkMode.IsZero() {
		out.WorkMode = base.WorkMode
	}
	if out.Locations.IsZero() {
		out.Locations = base.Locations
	}
	return out
}

type GoalsView struct {
	TargetRoles    []string
	Compensation   *Compensation
	EngagementType Buckets
	WorkingTime    Buckets
	WorkMode       Buckets
	TimeZones      []string
	Hours          *Hours
	Locations      PlaceBuckets
}

func Goals(p ProfileData) GoalsView {
	return GoalsView{p.TargetRoles, p.Compensation, p.EngagementType, p.WorkingTime, p.WorkMode, p.TimeZones, p.Hours, p.Locations}
}

func InheritedFields(p, base ProfileData) []string {
	var out []string
	add := func(own, inherited bool, name string) {
		if own && inherited {
			out = append(out, name)
		}
	}
	add(p.Compensation == nil, base.Compensation != nil, "compensation")
	add(p.EngagementType.IsZero(), !base.EngagementType.IsZero(), "engagementType")
	add(p.WorkingTime.IsZero(), !base.WorkingTime.IsZero(), "workingTime")
	add(p.Hours == nil, base.Hours != nil, "hours")
	add(p.WorkMode.IsZero(), !base.WorkMode.IsZero(), "workMode")
	add(p.Locations.IsZero(), !base.Locations.IsZero(), "locations")
	return out
}

type Profile struct {
	TargetRoles []string
	Keywords    []string
	AllOf       []string
}

type Filter struct {
	SQL  string
	Args []any
}

func Visibility(p Profile) Filter {
	hasRoles := len(p.TargetRoles) > 0
	hasKeywords := len(p.Keywords) > 0

	if !hasRoles && !hasKeywords {
		return Filter{SQL: "1=0"}
	}

	var clauses []string
	var args []any

	if hasRoles {
		parts := make([]string, 0, len(p.TargetRoles))
		for _, r := range p.TargetRoles {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			part, roleArgs := roleClause(r)
			parts = append(parts, part)
			args = append(args, roleArgs...)
		}
		if len(parts) > 0 {
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	if hasKeywords {
		parts := make([]string, 0, len(p.Keywords))
		for _, kw := range p.Keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			part, kwArgs := termClause(kw)
			parts = append(parts, part)
			args = append(args, kwArgs...)
		}
		if len(parts) > 0 {
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	for _, term := range p.AllOf {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		part, termArgs := termClause(term)
		clauses = append(clauses, part)
		args = append(args, termArgs...)
	}

	if len(clauses) == 0 {
		return Filter{SQL: "1=0"}
	}
	return Filter{SQL: strings.Join(clauses, " AND "), Args: args}
}

func NormalizeProfileData(pd *ProfileData) {
	pd.Compensation = normalizeCompensation(pd.Compensation)
	pd.Hours = normalizeHours(pd.Hours)
}

func NormalizeMeData(me *MeData) {
	me.Availability = normalizeAvailability(me.Availability)
	me.CurrentLocation = normalizeLocation(me.CurrentLocation)
	me.Demographics = normalizeDemographics(me.Demographics)
}

func normalizeCompensation(c *Compensation) *Compensation {
	if c == nil {
		return nil
	}
	c.DayRates = normalizeDayRates(c.DayRates)
	if c.CashMin == nil && c.CashTarget == nil && c.EquityOnTop == nil &&
		c.DayRates == nil && c.Currency == "" && c.GrossNet == "" && c.Period == "" && c.Crypto == "" {
		return nil
	}
	return c
}

func normalizeDayRates(dr *DayRates) *DayRates {
	if dr == nil {
		return nil
	}
	if dr.Min == nil && dr.Target == nil && dr.Currency == "" {
		return nil
	}
	return dr
}

func normalizeHours(h *Hours) *Hours {
	if h == nil {
		return nil
	}
	if h.Weekly == nil && h.WeeklyMin == nil && h.WeeklyMax == nil {
		return nil
	}
	return h
}

func normalizeAvailability(a *Availability) *Availability {
	if a == nil {
		return nil
	}
	if a.NoticeValue == nil && a.NoticeUnit == "" && a.StartDate == "" {
		return nil
	}
	return a
}

func normalizeLocation(l *Location) *Location {
	if l == nil {
		return nil
	}
	if l.Country == "" && l.City == "" {
		return nil
	}
	return l
}

func normalizeDemographics(d *Demographics) *Demographics {
	if d == nil {
		return nil
	}
	if d.Gender == "" && d.GenderDescription == "" && len(d.Race) == 0 && d.RaceDescription == "" &&
		d.Ethnicity == "" && d.Disability == "" && d.VeteranStatus == "" && d.CriminalRecord == "" {
		return nil
	}
	return d
}

func roleClause(role string) (string, []any) {
	return formsClause("title", role)
}

func termClause(term string) (string, []any) {
	return formsClause("{title tags text}", term)
}

func formsClause(columns, term string) (string, []any) {
	words := strings.Fields(listing.FTSNorm(term))
	if len(words) == 0 {
		return "1=0", nil
	}
	head := strings.Join(words[:len(words)-1], " ")
	if head != "" {
		head += " "
	}
	last := words[len(words)-1]
	forms := []string{last}
	if takesForms(term) {
		forms = wordForms(last)
	}
	phrases := make([]string, len(forms))
	for i, form := range forms {
		phrases[i] = `" ` + head + form + ` "`
	}
	matchArg := columns + ": (" + strings.Join(phrases, " OR ") + ")"
	return `v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?)`, []any{matchArg}
}

func takesForms(term string) bool {
	words := strings.FieldsFunc(term, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return false
	}
	runes := []rune(words[len(words)-1])
	if len(runes) <= 2 {
		return false
	}
	for _, r := range runes[1:] {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func wordForms(w string) []string {
	forms := []string{w}
	sibilant := hasAnySuffix(w, "s", "x", "z", "ch", "sh")
	if utf8.RuneCountInString(w) < 5 || !strings.HasSuffix(w, "s") || hasAnySuffix(w, "ss", "us", "is", "os") {
		if sibilant {
			return append(forms, w+"es")
		}
		return append(forms, w+"s")
	}
	forms = append(forms, w[:len(w)-1])
	if strings.HasSuffix(w, "es") && hasAnySuffix(w[:len(w)-2], "s", "x", "z", "ch", "sh") {
		forms = append(forms, w[:len(w)-2])
	}
	return forms
}

func hasAnySuffix(w string, suffixes ...string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(w, s) {
			return true
		}
	}
	return false
}

func LoadMe(ctx context.Context, database *db.DB) (MeData, error) {
	dataJSON, found, err := selectMe(ctx, database)
	if err != nil {
		return MeData{}, err
	}
	if !found {
		return MeData{}, nil
	}
	var me MeData
	if err := json.Unmarshal([]byte(dataJSON), &me); err != nil {
		return MeData{}, fmt.Errorf("candidate: decode me: %w", err)
	}
	return me, nil
}

func SaveMe(ctx context.Context, x execer, me MeData) error {
	NormalizeMeData(&me)
	data, err := json.Marshal(me)
	if err != nil {
		return fmt.Errorf("candidate: encode me: %w", err)
	}
	return upsertMe(ctx, x, string(data))
}

func ActiveProfileID(ctx context.Context, database *db.DB) (int64, error) {
	if id, ok := selectActiveProfileSetting(ctx, database); ok {
		return id, nil
	}
	return firstProfileID(ctx, database)
}

func LoadProfileData(ctx context.Context, database *db.DB, id int64) (ProfileData, error) {
	dataJSON, err := selectProfileData(ctx, database, id)
	if err != nil {
		return ProfileData{}, err
	}
	var pd ProfileData
	if err := json.Unmarshal([]byte(dataJSON), &pd); err != nil {
		return ProfileData{}, fmt.Errorf("candidate: decode profile %d: %w", id, err)
	}
	return pd, nil
}

func saveProfileRow(ctx context.Context, x execer, id int64, pd ProfileData) error {
	NormalizeProfileData(&pd)
	data, err := json.Marshal(pd)
	if err != nil {
		return fmt.Errorf("candidate: encode profile %d: %w", id, err)
	}
	return updateProfileData(ctx, x, id, string(data))
}

func createProfile(ctx context.Context, database *db.DB, name string) (int64, ProfileData, error) {
	pd := ProfileData{Name: name}
	data, err := json.Marshal(pd)
	if err != nil {
		return 0, ProfileData{}, fmt.Errorf("candidate: encode new profile: %w", err)
	}
	id, err := insertProfile(ctx, database, string(data))
	if err != nil {
		return 0, ProfileData{}, err
	}
	if err := setActiveProfile(ctx, database, id); err != nil {
		return 0, ProfileData{}, err
	}
	return id, pd, nil
}

func deleteProfile(ctx context.Context, database *db.DB, id int64) error {
	firstID, err := firstProfileID(ctx, database)
	if err != nil {
		return err
	}
	if id == firstID {
		return apperr.Invalid{Message: "The first profile cannot be deleted."}
	}
	if _, err := LoadProfileData(ctx, database, id); err != nil {
		return err
	}
	tx, err := beginTx(ctx, database)
	if err != nil {
		return fmt.Errorf("candidate: delete profile tx: %w", err)
	}
	defer tx.Rollback()
	if err := deleteProfileCascade(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("candidate: delete profile commit: %w", err)
	}
	activeID, _ := ActiveProfileID(ctx, database)
	if activeID == 0 || activeID == id {
		setActiveProfile(ctx, database, firstID)
	}
	return nil
}

func listProfiles(ctx context.Context, database *db.DB) (ProfileListResp, error) {
	activeID, _ := ActiveProfileID(ctx, database)
	rows, err := selectProfiles(ctx, database)
	if err != nil {
		return ProfileListResp{}, err
	}
	items := []ProfileListItem{}
	for _, r := range rows {
		var pd ProfileData
		json.Unmarshal([]byte(r.data), &pd)
		items = append(items, ProfileListItem{ID: r.id, Name: pd.Name, Active: r.id == activeID})
	}
	return ProfileListResp{Profiles: items}, nil
}

func loadProfileDetail(ctx context.Context, database *db.DB, id int64, own ProfileData) (ProfileDetailResp, error) {
	firstID, err := firstProfileID(ctx, database)
	if err != nil {
		return ProfileDetailResp{}, err
	}
	firstPD, err := LoadProfileData(ctx, database, firstID)
	if err != nil {
		return ProfileDetailResp{}, err
	}
	activeID, _ := ActiveProfileID(ctx, database)
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

func listQA(ctx context.Context, database *db.DB) (QAListResp, error) {
	items, err := selectQAs(ctx, database)
	if err != nil {
		return QAListResp{}, err
	}
	if items == nil {
		items = []QAItem{}
	}
	return QAListResp{Items: items}, nil
}

func createQA(ctx context.Context, database *db.DB, req QACreateReq) (QAItem, error) {
	q := strings.TrimSpace(req.Question)
	if q == "" {
		return QAItem{}, apperr.Invalid{Message: "Question must not be empty."}
	}
	a := strings.TrimSpace(req.Answer)
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := insertQA(ctx, database, q, a, now)
	if err != nil {
		return QAItem{}, err
	}
	return QAItem{ID: id, Question: q, Answer: a, CreatedAt: now, UpdatedAt: now}, nil
}

func patchQA(ctx context.Context, database *db.DB, req QAPatchReq) (QAItem, error) {
	if req.ID == 0 {
		return QAItem{}, apperr.Invalid{Message: "ID is required."}
	}
	current, err := selectQA(ctx, database, req.ID)
	if err != nil {
		return QAItem{}, err
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
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := updateQA(ctx, database, current); err != nil {
		return QAItem{}, err
	}
	return current, nil
}

func removeQA(ctx context.Context, database *db.DB, id int64) error {
	if id == 0 {
		return apperr.Invalid{Message: "ID is required."}
	}
	return deleteQA(ctx, database, id)
}
