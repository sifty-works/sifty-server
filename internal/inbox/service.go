package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/places"
)

type Filters struct {
	Published  int
	Remote     bool
	Leadership bool
	Sources    []string
	Unread     bool
	Fit        []string
	inputs     int64
}

type Stage string

const (
	StageInbox     Stage = "inbox"
	StageShortlist Stage = "shortlist"
	StageApplied   Stage = "applied"
	StageDismissed Stage = "dismissed"
)

type ConfirmDialog string

const (
	ConfirmOutcome ConfirmDialog = "outcome"
	ConfirmDelete  ConfirmDialog = "delete"
)

type Row struct {
	ID           int64    `json:"id"`
	Company      string   `json:"company"`
	Title        string   `json:"title"`
	Location     string   `json:"location"`
	LocationFull string   `json:"locationFull,omitempty"`
	Published    string   `json:"published"`
	Industries   []string `json:"industries"`
	Unread       bool     `json:"unread"`
	Stage        Stage    `json:"stage"`
	Reason       string   `json:"reason,omitempty"`
	Applied      string   `json:"applied,omitempty"`
	Expired      bool     `json:"expired,omitempty"`
	Fit          FitMark  `json:"fit"`
}

type FitMark struct {
	Score            *int   `json:"score"`
	Bucket           string `json:"bucket"`
	Text             string `json:"text"`
	Dimmed           bool   `json:"dimmed"`
	RequirementsText string `json:"requirementsText"`
}

type RequirementGroup struct {
	Label string                `json:"label"`
	Items []fit.RequirementLine `json:"items"`
}

type Band struct {
	Score            *int               `json:"score"`
	Bucket           string             `json:"bucket"`
	Text             string             `json:"text"`
	Tiles            []fit.Tile         `json:"tiles"`
	RequirementsText string             `json:"requirementsText"`
	RequirementCount int                `json:"requirementCount"`
	Groups           []RequirementGroup `json:"groups"`
}

func reqText(state string, reqRead bool, reqOK, reqNo, reqMaybe int, inReadingSet bool) string {
	if state == fit.NoText {
		return "Full job description is unavailable"
	}
	if !reqRead {
		if inReadingSet {
			return "Analysing requirements…"
		}
		return "Not analysed yet"
	}
	total := reqOK + reqNo + reqMaybe
	if total == 0 {
		return "No requirements stated"
	}
	if reqOK == 0 && reqNo == 0 {
		return "Not checked yet"
	}
	return fmt.Sprintf("✓ %d · ✕ %d · ? %d", reqOK, reqNo, reqMaybe)
}

func queryReadingSet(ctx context.Context, database *db.DB) (map[int64]bool, []int64, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT id, kind, subject FROM job WHERE kind IN ('read_vacancy','shortlist_check') AND state IN ('queued','running')
		 ORDER BY priority DESC, created ASC, id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("inbox: reading set: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	var order []int64
	for rows.Next() {
		var id int64
		var kind, subject string
		if err := rows.Scan(&id, &kind, &subject); err != nil {
			return nil, nil, fmt.Errorf("inbox: reading set: %w", err)
		}
		order = append(order, id)
		part, _, _ := strings.Cut(subject, "/")
		if vacancyID, err := strconv.ParseInt(part, 10, 64); err == nil && kind == "read_vacancy" {
			out[vacancyID] = true
		}
	}
	return out, order, rows.Close()
}

func fitMark(s fit.Stored, inputs int64, vacancyID int64, readingSet map[int64]bool) FitMark {
	m := FitMark{Bucket: "Not scored", Text: fit.Texts[s.State.String], Dimmed: s.State.Valid && !s.Current(inputs)}
	if s.State.String == fit.Scored {
		n := int(s.Score.Int64)
		m.Score, m.Bucket = &n, fit.Bucket(n)
		m.Text = fmt.Sprintf("Fits you %d: %s", n, m.Bucket)
	}
	m.RequirementsText = reqText(s.State.String, s.ReqOK.Valid,
		int(s.ReqOK.Int64), int(s.ReqNo.Int64), int(s.ReqMaybe.Int64),
		readingSet[vacancyID])
	return m
}

func fitInputs(ctx context.Context, database *db.DB, profileID int64, r fit.Rules) int64 {
	p, _ := fit.LoadProfile(ctx, database, profileID, r)
	return p.Inputs
}

type Counts struct {
	Inbox     int `json:"inbox"`
	Shortlist int `json:"shortlist"`
	Applied   int `json:"applied"`
	Dismissed int `json:"dismissed"`
	Archive   int `json:"archive"`
}

func toRow(rr repoRow, now time.Time, inputs int64, readingSet map[int64]bool) Row {
	nowStr := now.UTC().Format(time.RFC3339)
	var industries []string
	if rr.industries != "" && rr.industries != "[]" {
		json.Unmarshal([]byte(rr.industries), &industries)
	}
	if industries == nil {
		industries = []string{}
	}
	loc, full := "Location unstated", ""
	if rr.location != "" {
		loc, full = places.Short(strings.Split(rr.location, places.Separator))
	}
	return Row{
		ID:           rr.id,
		Company:      rr.company,
		Title:        rr.title,
		Location:     loc,
		LocationFull: full,
		Published:    publicationLine(rr.publishedAt, rr.publishedBasis, now),
		Industries:   industries,
		Unread:       rr.unread,
		Stage:        Stage(rr.stage),
		Reason:       rr.reason,
		Applied:      appliedText(rr.appliedAt, rr.appliedName, rr.stage, now),
		Expired:      rr.expiresAt != "" && rr.expiresAt <= nowStr,
		Fit:          fitMark(rr.fit, inputs, rr.id, readingSet),
	}
}

func profileVisibility(ctx context.Context, database *db.DB, profileID int64) candidate.Filter {
	p, err := candidate.ProfileSearch(ctx, database, profileID)
	if err != nil {
		return candidate.Filter{SQL: "1=0"}
	}
	return candidate.Visibility(p)
}

func buildCounts(cm map[string]int) Counts {
	return Counts{
		Inbox:     cm["inbox"],
		Shortlist: cm["shortlist"],
		Applied:   cm["applied"],
		Dismissed: cm["dismissed"],
		Archive:   cm["applied"] + cm["dismissed"],
	}
}

func loadCounts(ctx context.Context, database *db.DB, now time.Time, profileID int64) (Counts, error) {
	vis := profileVisibility(ctx, database, profileID)
	cm, err := stageCounts(ctx, database, now, profileID, vis)
	if err != nil {
		return Counts{}, err
	}
	return buildCounts(cm), nil
}

func rowAndCounts(ctx context.Context, database *db.DB, now func() time.Time, profileID, vacancyID int64, r fit.Rules) (Row, Counts, error) {
	vis := profileVisibility(ctx, database, profileID)
	_, row, _, err := loadVacancyFull(ctx, database, profileID, vacancyID)
	if err != nil {
		return Row{}, Counts{}, err
	}
	t := now()
	cm, err := stageCounts(ctx, database, t, profileID, vis)
	if err != nil {
		return Row{}, Counts{}, err
	}
	rs, _, err := queryReadingSet(ctx, database)
	if err != nil {
		return Row{}, Counts{}, err
	}
	return toRow(row, t, fitInputs(ctx, database, profileID, r), rs), buildCounts(cm), nil
}

func list(ctx context.Context, database *db.DB, now func() time.Time, profileID int64, stage, q, sort string, industries []string, f Filters, r fit.Rules) ([]Row, Counts, []IndustryCount, []SourceCount, int, error) {
	vis := profileVisibility(ctx, database, profileID)
	f.inputs = fitInputs(ctx, database, profileID, r)
	t := now()
	cm, err := stageCounts(ctx, database, t, profileID, vis)
	if err != nil {
		return nil, Counts{}, nil, nil, 0, err
	}
	indCounts, err := industryCounts(ctx, database, t, profileID, stage, q, vis, f)
	if err != nil {
		return nil, Counts{}, nil, nil, 0, err
	}
	srcCounts, err := sourceCounts(ctx, database, t, profileID, stage, q, vis, industries, f)
	if err != nil {
		return nil, Counts{}, nil, nil, 0, err
	}
	rs, _, err := queryReadingSet(ctx, database)
	if err != nil {
		return nil, Counts{}, nil, nil, 0, err
	}
	raw, err := listRows(ctx, database, t, profileID, stage, q, sort, vis, f, industries...)
	if err != nil {
		return nil, Counts{}, nil, nil, 0, err
	}
	rows := make([]Row, len(raw))
	for i, rr := range raw {
		rows[i] = toRow(rr, t, f.inputs, rs)
	}
	total := cm[stage]
	return rows, buildCounts(cm), indCounts, srcCounts, total, nil
}

func applyDecision(ctx context.Context, database *db.DB, now func() time.Time, enqueue func(kind, subject string) (int64, error),
	vacancyID, profileID int64, from, to, reason string, confirmed bool, r fit.Rules) (Row, Counts, error) {

	var currentStage string
	err := database.QueryRowContext(ctx,
		`SELECT COALESCE(d.stage,'inbox') FROM vacancy v LEFT JOIN decision d ON d.vacancy=v.id AND d.profile=? WHERE v.id=?`,
		profileID, vacancyID).Scan(&currentStage)
	if err == sql.ErrNoRows {
		return Row{}, Counts{}, apperr.NotFound{Message: "Role not found."}
	}
	if err != nil {
		return Row{}, Counts{}, fmt.Errorf("inbox: decision: load stage: %w", err)
	}

	if from != currentStage {
		return Row{}, Counts{}, apperr.Conflict{
			Message: fmt.Sprintf("job %d changed while this outcome was being saved; reload and try again", vacancyID),
		}
	}

	if from == to {
		return rowAndCounts(ctx, database, now, profileID, vacancyID, r)
	}

	if err := validateMove(from, to, reason, confirmed); err != nil {
		return Row{}, Counts{}, err
	}

	nowStr := now().UTC().Format(time.RFC3339)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return Row{}, Counts{}, fmt.Errorf("inbox: decision: begin tx: %w", err)
	}
	defer tx.Rollback()

	switch to {
	case "inbox":
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM decision WHERE profile=? AND vacancy=?`, profileID, vacancyID); err != nil {
			return Row{}, Counts{}, fmt.Errorf("inbox: decision: delete: %w", err)
		}
	default:
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO decision (profile, vacancy, stage, reason, decided_at) VALUES (?,?,?,?,?)`,
			profileID, vacancyID, to, reason, nowStr); err != nil {
			return Row{}, Counts{}, fmt.Errorf("inbox: decision: upsert: %w", err)
		}
		if to == "applied" {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO application (vacancy, applied_at, profile, profile_name) SELECT ?,?,id,json_extract(data,'$.name') FROM profile WHERE id=?`,
				vacancyID, nowStr, profileID); err != nil {
				return Row{}, Counts{}, fmt.Errorf("inbox: decision: application: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return Row{}, Counts{}, fmt.Errorf("inbox: decision: commit: %w", err)
	}
	if to == string(StageShortlist) {
		loadCheck(ctx, database, enqueue, profileID, vacancyID, to, true)
	}
	return rowAndCounts(ctx, database, now, profileID, vacancyID, r)
}

func validateMove(from, to, reason string, confirmed bool) error {
	inv := apperr.Invalid{Message: "the role is no longer awaiting an application decision; reload and try again"}
	if reason != "" && !(from == "shortlist" && to == "dismissed") {
		return inv
	}
	if len([]rune(reason)) > 200 {
		return apperr.Invalid{Message: "Reason must be at most 200 characters."}
	}
	switch from + "→" + to {
	case "inbox→shortlist", "inbox→dismissed",
		"shortlist→inbox", "shortlist→dismissed",
		"applied→shortlist", "dismissed→inbox":
		return nil
	case "shortlist→applied":
		if !confirmed {
			return inv
		}
		return nil
	}
	return inv
}

func publicationLine(publishedAt, publishedBasis string, now time.Time) string {
	if publishedAt == "" {
		return "Publication date not reported"
	}
	t, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return "Publication date not reported"
	}
	loc := now.Location()
	dateStr := t.In(loc).Format("2 Jan 2006")
	if publishedBasis != "" {
		return "Published " + dateStr + " · " + ageText(t, now)
	}
	return "Source date " + dateStr + " (unverified)"
}

func ageText(published, now time.Time) string {
	loc := now.Location()
	py, pm, pd := published.In(loc).Date()
	ny, nm, nd := now.In(loc).Date()
	pubDate := time.Date(py, pm, pd, 0, 0, 0, 0, loc)
	nowDate := time.Date(ny, nm, nd, 0, 0, 0, 0, loc)
	days := int(nowDate.Sub(pubDate).Hours() / 24)
	if days < 0 {
		days = 0
	}
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "1 day"
	case days < 14:
		return fmt.Sprintf("%d days", days)
	case days < 63:
		return fmt.Sprintf("%d weeks", days/7)
	default:
		return fmt.Sprintf("%d months", days/30)
	}
}

func appliedText(appliedAt, profileName, stage string, now time.Time) string {
	if appliedAt == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, appliedAt)
	if err != nil {
		return ""
	}
	date := t.In(now.Location()).Format("2 Jan 2006")
	if stage == "applied" {
		return "Applied " + date
	}
	return "Applied " + date + " from " + profileName
}

func emptyText(rowCount int, stage string, hasFilters bool) string {
	if rowCount > 0 {
		return ""
	}
	if hasFilters {
		return "No matching roles. Adjust your filters."
	}
	switch stage {
	case "shortlist":
		return "Nothing shortlisted yet — press Y in Inbox."
	case "applied":
		return "No applications yet."
	case "dismissed":
		return "No dismissed roles."
	}
	return "Inbox zero. Collect more jobs in Settings → Sources."
}

type Outcome struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	To        Stage  `json:"to,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type Action struct {
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	To       Stage         `json:"to,omitempty"`
	URL      string        `json:"url,omitempty"`
	Message  string        `json:"message,omitempty"`
	Confirm  ConfirmDialog `json:"confirm,omitempty"`
	Outcomes []Outcome     `json:"outcomes,omitempty"`
}

type ListingView struct {
	State string `json:"state"`
	HTML  string `json:"html"`
	Empty string `json:"empty,omitempty"`
}

type VacancySource struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type OfferView struct {
	Tiles []listing.OfferTile `json:"tiles"`
	Items []string            `json:"items"`
	Empty string              `json:"empty,omitempty"`
}

type VacancyView struct {
	Row
	Sources  []VacancySource `json:"sources"`
	Original string          `json:"original"`
	Status   string          `json:"status,omitempty"`
	Actions  []Action        `json:"actions"`
	Listing  ListingView     `json:"listing"`
	Band     Band            `json:"band"`
	Offer    OfferView       `json:"offer"`
	Check    *CheckView      `json:"check"`
}

func offerView(body string, tiles []fit.Tile) OfferView {
	o := listing.ReadOffer(body)
	v := OfferView{Tiles: []listing.OfferTile{}, Items: []string{}}
	if !o.Found {
		v.Empty = "No Compensation or Benefits section recognised in this listing."
		return v
	}
	if t, ok := fit.TileFor(tiles, "money"); ok && t.Value != "" && t.Value != "Not stated" {
		v.Tiles = append(v.Tiles, listing.OfferTile{Key: "salary", Label: "Base salary", Value: t.Value})
	}
	v.Tiles = append(v.Tiles, o.Tiles...)
	v.Items = append(v.Items, o.Items...)
	if len(o.Tiles) == 0 && len(o.Items) == 0 {
		v.Empty = "Section found but no specific items matched."
	}
	return v
}

func listingState(textState string) string {
	switch listing.TextState(textState) {
	case listing.TextFull:
		return "Full text"
	case listing.TextSummary:
		return "Listing summary only"
	case listing.TextBlocked:
		return "Blocked by site"
	case listing.TextRemoved:
		return "Text removed after 60 days unseen; it returns if the source shows the role again"
	}
	return "Not fetched"
}

func toVacancySource(src string, names map[string]string) VacancySource {
	name, srcURL := names[src], ""
	if u, err := url.Parse(src); err == nil && u.Host != "" {
		if name == "" {
			name = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		}
		if u.Scheme == "http" || u.Scheme == "https" {
			srcURL = src
		}
	}
	if name == "" {
		name = src
	}
	return VacancySource{Name: name, URL: srcURL}
}

func statusText(stage, reason, appliedAt string, t time.Time) string {
	if stage == "dismissed" {
		if reason != "" {
			return "Dismissed — " + reason
		}
		return "Dismissed"
	}
	if stage != "applied" || appliedAt == "" {
		return ""
	}
	at, err := time.Parse(time.RFC3339, appliedAt)
	if err != nil {
		return ""
	}
	return "Applied " + at.In(t.Location()).Format("2 Jan 2006")
}

func validApplyURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.User == nil
}

var outcomeButtons = []Outcome{
	{Key: "not-yet", Label: "Not yet"},
	{Key: "stale", Label: "No, vacancy is stale", To: StageDismissed, Reason: "vacancy is stale"},
	{Key: "closed", Label: "No, vacancy is closed", To: StageDismissed, Reason: "vacancy is closed"},
	{Key: "unsuitable", Label: "No, not suitable", To: StageDismissed, Reason: "not suitable"},
	{Key: "applied", Label: "Yes, applied", To: StageApplied, Confirmed: true},
}

func stageActions(stage, vacURL, applyURL, company string) []Action {
	switch stage {
	case "inbox":
		out := make([]Action, 0, 3)
		if company != "" {
			out = append(out, Action{Key: "block", Label: "Block company"})
		}
		return append(out,
			Action{Key: "dismiss", Label: "Dismiss", To: StageDismissed},
			Action{Key: "select", Label: "Select", To: StageShortlist})
	case "shortlist":
		apply := buildApplyAction(vacURL, applyURL)
		return []Action{
			{Key: "inbox", Label: "Move to Inbox", To: StageInbox},
			apply,
			{Key: "confirm", Label: "Confirm application…", To: StageApplied, Confirm: ConfirmOutcome,
				Outcomes: outcomeButtons},
			{Key: "delete", Label: "Delete role…", Confirm: ConfirmDelete},
		}
	case "applied":
		return []Action{
			{Key: "undo", Label: "Undo", To: StageShortlist},
			{Key: "delete", Label: "Delete role…", Confirm: ConfirmDelete},
		}
	case "dismissed":
		return []Action{
			{Key: "restore", Label: "Restore to Inbox", To: StageInbox},
			{Key: "delete", Label: "Delete role…", Confirm: ConfirmDelete},
		}
	}
	return []Action{}
}

func buildApplyAction(vacURL, applyURL string) Action {
	if validApplyURL(applyURL) {
		return Action{Key: "apply", Label: "Apply", URL: applyURL, Confirm: ConfirmOutcome, Outcomes: outcomeButtons}
	}
	if validApplyURL(vacURL) {
		return Action{Key: "apply", Label: "Apply", URL: vacURL, Confirm: ConfirmOutcome, Outcomes: outcomeButtons}
	}
	return Action{Key: "apply", Label: "Apply",
		Message:  "No valid employer or original listing URL is available. The role stays in Shortlist.",
		Confirm:  ConfirmOutcome,
		Outcomes: outcomeButtons,
	}
}

func vacancyView(ctx context.Context, database *db.DB, now func() time.Time, enqueue func(kind, subject string) (int64, error),
	vacancyID, profileID int64, sourceNames map[string]string, r fit.Rules) (VacancyView, error) {

	t := now()
	stage, row, vd, err := loadVacancyFull(ctx, database, profileID, vacancyID)
	if err != nil {
		return VacancyView{}, err
	}

	sources := make([]VacancySource, 0, len(vd.sources))
	for _, src := range vd.sources {
		sources = append(sources, toVacancySource(src, sourceNames))
	}

	html, err := catalog.Body(vd.body)
	if err != nil {
		return VacancyView{}, fmt.Errorf("inbox: view: body: %w", err)
	}
	lv := ListingView{State: listingState(vd.textState), HTML: html}
	if html == "" {
		lv.Empty = "A full description has not been retrieved. Open the original listing; missing text is not an empty job."
	}

	f, err := fit.Band(ctx, database, profileID, vacancyID, r)
	if err != nil {
		return VacancyView{}, err
	}
	cs, err := loadCheck(ctx, database, enqueue, profileID, vacancyID, stage, false)
	if err != nil {
		return VacancyView{}, err
	}
	rs, queue, err := queryReadingSet(ctx, database)
	if err != nil {
		return VacancyView{}, err
	}
	groups := []RequirementGroup{}
	if f.ReqRead && len(f.Requirements) > 0 {
		var req, nice []fit.RequirementLine
		for _, r := range f.Requirements {
			if r.Level == "required" {
				req = append(req, r)
			} else {
				nice = append(nice, r)
			}
		}
		if len(req) > 0 {
			groups = append(groups, RequirementGroup{"Required", req})
		}
		if len(nice) > 0 {
			groups = append(groups, RequirementGroup{"Nice to have", nice})
		}
	}
	band := Band{
		Bucket: f.Bucket, Text: f.Text, Tiles: f.Tiles,
		RequirementsText: reqText(f.State, f.ReqRead, f.ReqOK, f.ReqNo, f.ReqMaybe, rs[vacancyID]),
		RequirementCount: len(f.Requirements),
		Groups:           groups,
	}
	if f.State == fit.Scored {
		band.Score, band.Text = &f.Score, fmt.Sprintf("Fits you %d /100", f.Score)
	}
	apiRow := toRow(row, t, fitInputs(ctx, database, profileID, r), rs)
	return VacancyView{
		Row:      apiRow,
		Sources:  sources,
		Original: vd.url,
		Status:   statusText(stage, row.reason, row.appliedAt, t),
		Actions:  stageActions(stage, vd.url, vd.applyURL, apiRow.Company),
		Listing:  lv,
		Band:     band,
		Offer:    offerView(html, f.Tiles),
		Check:    cs.view(f.Requirements, queue, t),
	}, nil
}
