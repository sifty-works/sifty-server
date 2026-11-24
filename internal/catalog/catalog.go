package catalog

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/source"
)

type Outcome int

const (
	OutcomeNew Outcome = iota + 1
	OutcomeUpdated
	OutcomeUnchanged
	OutcomeMerged
	OutcomeTombstoned
)

type Catalog struct {
	db  *db.DB
	now func() time.Time
}

func New(database *db.DB, now func() time.Time) *Catalog {
	return &Catalog{db: database, now: now}
}

func Encode(html string) (body []byte, hash string, err error) {
	plain := listing.PlainText(html)
	hash = contentHash(plain)
	body, err = compressBody(html)
	if err != nil {
		return nil, "", fmt.Errorf("catalog: encode: %w", err)
	}
	return body, hash, nil
}

func Body(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	r := flate.NewReader(bytes.NewReader(blob))
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("catalog: body: decompress: %w", err)
	}
	return string(b), nil
}

func WriteFTS(ctx context.Context, tx *sql.Tx, id int64, title, tagsJSON string, body []byte) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM vacancy_fts WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("catalog: fts delete %d: %w", id, err)
	}
	var plainText string
	if len(body) > 0 {
		h, err := Body(body)
		if err != nil {
			return fmt.Errorf("catalog: fts body %d: %w", id, err)
		}
		plainText = listing.PlainText(h)
	}
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)
	tagsText := strings.Join(tags, " ")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO vacancy_fts(rowid, title, tags, text) VALUES(?, ?, ?, ?)`,
		id, listing.FTSNorm(title), listing.FTSNorm(tagsText), listing.FTSNorm(plainText)); err != nil {
		return fmt.Errorf("catalog: fts insert %d: %w", id, err)
	}
	return nil
}

type resolved struct {
	canonURL, canonApply, company, identity string
	removed                                 bool
	matchIDs                                []int64
}

func resolve(ctx context.Context, tx *sql.Tx, p listing.Posting) (resolved, error) {
	canon, err := listing.Canonical(p.URL)
	if err != nil {
		return resolved{}, fmt.Errorf("canonical url: %w", err)
	}
	var apply string
	for _, raw := range []string{p.ApplyURL, p.URL} {
		if raw != "" {
			if ca, ok := source.ConcreteATS(raw); ok {
				apply = ca
				break
			}
		}
	}
	co, id := p.Company, listing.Identity(p.Company, p.Title)
	gone, err := anyTombstoned(ctx, tx, tombstoneKeys(canon, p.URL, p.ApplyURL))
	if err != nil {
		return resolved{}, fmt.Errorf("tombstone: %w", err)
	}
	if gone {
		return resolved{removed: true}, nil
	}
	ms, err := findMatches(ctx, tx, canon, apply, apply, id, co)
	if err != nil {
		return resolved{}, fmt.Errorf("match: %w", err)
	}
	return resolved{canonURL: canon, canonApply: apply, company: co, identity: id, matchIDs: ms}, nil
}

func (c *Catalog) Upsert(ctx context.Context, p listing.Posting, src string) (int64, Outcome, error) {
	now := c.now().UTC()
	nowStr := now.Format(time.RFC3339)

	bodyHTML := listing.Normalize(p.Text)
	newBody, newHash, err := Encode(bodyHTML)
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: %w", err)
	}

	tagsJSON := marshalTags(p.Tags)
	fieldsJSON := marshalFields(p.Fields)
	publishedAt := ""
	publishedBasis := ""
	if !p.Published.IsZero() && !p.Published.After(now) {
		publishedAt = formatTime(p.Published)
		publishedBasis = p.PublishedBasis
	}
	expiresAt := formatTime(p.Expires)

	textState := string(p.TextState)
	if textState == "" {
		textState = string(listing.TextNotFetched)
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: begin tx: %w", err)
	}
	defer tx.Rollback()

	r, err := resolve(ctx, tx, p)
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: %w", err)
	}
	if r.removed {
		return 0, OutcomeTombstoned, nil
	}
	matchIDs := r.matchIDs

	if len(matchIDs) == 0 {
		row := vacancyRow{
			identity:       r.identity,
			title:          p.Title,
			company:        r.company,
			url:            r.canonURL,
			applyURL:       r.canonApply,
			location:       p.Location,
			tagsJSON:       tagsJSON,
			fieldsJSON:     fieldsJSON,
			publishedAt:    publishedAt,
			publishedBasis: publishedBasis,
			expiresAt:      expiresAt,
			firstSeen:      nowStr,
			lastSeen:       nowStr,
			textState:      textState,
			contentHash:    newHash,
			version:        1,
			body:           newBody,
		}
		if p.Requested {
			row.detailRequestedAt = nowStr
		}
		id, err := save(ctx, tx, row)
		if err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: insert: %w", err)
		}
		if err := WriteFTS(ctx, tx, id, row.title, row.tagsJSON, newBody); err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: fts: %w", err)
		}
		if err := saveSource(ctx, tx, id, src, r.canonURL, nowStr); err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: insert source: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: commit: %w", err)
		}
		return id, OutcomeNew, nil
	}

	if len(matchIDs) > 1 {
		sort.Slice(matchIDs, func(i, j int) bool { return matchIDs[i] < matchIDs[j] })
		if err := performMerge(ctx, tx, matchIDs); err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: merge: %w", err)
		}
	}
	vacancyID := matchIDs[0]

	ex, err := loadVacancy(ctx, tx, vacancyID)
	if err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: load: %w", err)
	}

	upd := ex
	if p.Requested {
		upd.detailRequestedAt = nowStr
	}

	upd.title = p.Title
	if r.company != "" && (!p.CompanyGuessed || ex.company == "") {
		upd.company = r.company
		upd.identity = listing.Identity(r.company, p.Title)
	} else {
		upd.identity = listing.Identity(ex.company, p.Title)
	}
	if p.Location != "" {
		upd.location = p.Location
	}
	if len(p.Tags) > 0 {
		upd.tagsJSON = tagsJSON
	}
	upd.fieldsJSON = mergeFields(ex.fieldsJSON, fieldsJSON)
	if ex.applyURL == "" && r.canonApply != "" {
		upd.applyURL = r.canonApply
	}
	if ex.publishedAt == "" && publishedAt != "" {
		upd.publishedAt = publishedAt
		upd.publishedBasis = publishedBasis
	}
	if expiresAt != "" {
		upd.expiresAt = expiresAt
	}

	textChanged := false
	if shouldReplaceText(listing.TextState(ex.textState), p.Text, p.TextState, ex.contentHash, newHash) {
		upd.contentHash = newHash
		upd.body = newBody
		upd.textState = textState
		upd.version = ex.version + 1
		textChanged = true
	}
	exHasText := listing.TextState(ex.textState) == listing.TextFull ||
		listing.TextState(ex.textState) == listing.TextSummary
	if !textChanged && p.TextState == listing.TextBlocked && !exHasText {
		upd.textState = textState
	}
	upd.lastSeen = nowStr

	titleChanged := p.Title != ex.title
	tagsChanged := len(p.Tags) > 0 && tagsJSON != ex.tagsJSON

	if _, err := save(ctx, tx, upd); err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: update: %w", err)
	}

	if titleChanged || tagsChanged || textChanged {
		if err := WriteFTS(ctx, tx, vacancyID, upd.title, upd.tagsJSON, upd.body); err != nil {
			return 0, 0, fmt.Errorf("catalog: upsert: fts: %w", err)
		}
	}
	if err := saveSource(ctx, tx, vacancyID, src, r.canonURL, nowStr); err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: source: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("catalog: upsert: commit: %w", err)
	}

	if len(matchIDs) > 1 {
		return vacancyID, OutcomeMerged, nil
	}
	if textChanged {
		return vacancyID, OutcomeUpdated, nil
	}
	return vacancyID, OutcomeUnchanged, nil
}

type vacancyRow struct {
	id                int64
	identity          string
	title             string
	company           string
	url               string
	applyURL          string
	location          string
	tagsJSON          string
	fieldsJSON        string
	publishedAt       string
	publishedBasis    string
	expiresAt         string
	firstSeen         string
	lastSeen          string
	seenAt            string
	textState         string
	contentHash       string
	version           int
	body              []byte
	detailRequestedAt string
}

func save(ctx context.Context, tx *sql.Tx, v vacancyRow) (int64, error) {
	if v.body == nil {
		v.body = []byte{}
	}
	if v.id == 0 {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO vacancy
				(identity, title, company, url, apply_url, location, tags, source_fields,
				 published_at, published_basis, expires_at,
				 first_seen, last_seen, text_state, content_hash, version, body,
				 detail_requested_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			v.identity, v.title, v.company, v.url, v.applyURL, v.location,
			v.tagsJSON, v.fieldsJSON,
			v.publishedAt, v.publishedBasis, v.expiresAt,
			v.firstSeen, v.lastSeen, v.textState, v.contentHash, v.version, v.body,
			v.detailRequestedAt)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE vacancy SET
			identity=?, title=?, company=?, location=?, tags=?, source_fields=?,
			apply_url=?, published_at=?, published_basis=?, expires_at=?,
			last_seen=?, seen_at=?, text_state=?, content_hash=?, version=?, body=?,
			detail_requested_at=?
		WHERE id=?`,
		v.identity, v.title, v.company, v.location, v.tagsJSON, v.fieldsJSON,
		v.applyURL, v.publishedAt, v.publishedBasis, v.expiresAt,
		v.lastSeen, v.seenAt, v.textState, v.contentHash, v.version, v.body,
		v.detailRequestedAt, v.id)
	return v.id, err
}

func loadVacancy(ctx context.Context, tx *sql.Tx, id int64) (vacancyRow, error) {
	var ex vacancyRow
	err := tx.QueryRowContext(ctx, `
		SELECT id, identity, title, company, url, apply_url, location, tags, source_fields,
		       published_at, published_basis, expires_at,
		       first_seen, last_seen, seen_at, text_state, content_hash, version, body,
		       detail_requested_at
		FROM vacancy WHERE id = ?`, id).Scan(
		&ex.id, &ex.identity, &ex.title, &ex.company, &ex.url, &ex.applyURL, &ex.location,
		&ex.tagsJSON, &ex.fieldsJSON,
		&ex.publishedAt, &ex.publishedBasis, &ex.expiresAt,
		&ex.firstSeen, &ex.lastSeen, &ex.seenAt, &ex.textState, &ex.contentHash, &ex.version, &ex.body,
		&ex.detailRequestedAt)
	return ex, err
}

func saveSource(ctx context.Context, tx *sql.Tx, vacancyID int64, src, u, seenAt string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO vacancy_source (vacancy_id, source, url, seen_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET seen_at = excluded.seen_at`,
		vacancyID, src, u, seenAt)
	return err
}

func findMatches(ctx context.Context, tx *sql.Tx, canonURL, atsApply, canonApply, identity, company string) ([]int64, error) {
	var ids []int64

	var aid int64
	err := tx.QueryRowContext(ctx, `SELECT vacancy_id FROM vacancy_source WHERE url = ?`, canonURL).Scan(&aid)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("match by url: %w", err)
	}
	if err == nil {
		ids = appendUnique(ids, aid)
	}

	newHost := urlHost(canonURL)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, url, apply_url FROM vacancy
		WHERE (? != '' AND apply_url = ?) OR (identity = ? AND company != '')`,
		atsApply, atsApply, identity)
	if err != nil {
		return nil, fmt.Errorf("match by apply/identity: %w", err)
	}
	for rows.Next() {
		var rid int64
		var rURL, rApply string
		if err := rows.Scan(&rid, &rURL, &rApply); err != nil {
			rows.Close()
			return nil, err
		}
		if atsApply != "" && rApply == atsApply {
			ids = appendUnique(ids, rid)
			continue
		}
		if company == "" {
			continue
		}
		if urlHost(rURL) == newHost {
			continue
		}
		if canonApply != "" && rApply != "" && canonApply != rApply && urlHost(canonApply) == urlHost(rApply) {
			continue
		}
		ids = appendUnique(ids, rid)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	return ids, nil
}

func shouldReplaceText(storedState listing.TextState, newText string, newState listing.TextState, oldHash, newHash string) bool {
	if newText == "" {
		return false
	}
	if newState == listing.TextBlocked {
		return false
	}
	if storedState == listing.TextRemoved || storedState == listing.TextNotFetched {
		return true
	}
	if storedState == listing.TextFull && newState == listing.TextSummary {
		return false
	}
	return oldHash != newHash
}

func compressBody(html string) ([]byte, error) {
	if html == "" {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(html)); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func contentHash(plainText string) string {
	plainText = strings.Join(strings.Fields(plainText), " ")
	if plainText == "" {
		return ""
	}
	h := sha256.Sum256([]byte(plainText))
	return hex.EncodeToString(h[:])
}

func marshalTags(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func marshalFields(fields map[string]string) string {
	if len(fields) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(fields)
	return string(b)
}

func mergeFields(existing, incoming string) string {
	var base, over map[string]string
	json.Unmarshal([]byte(existing), &base)
	json.Unmarshal([]byte(incoming), &over)
	if base == nil {
		base = make(map[string]string)
	}
	for k, v := range over {
		base[k] = v
	}
	b, _ := json.Marshal(base)
	return string(b)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func tombstoneKeys(canonURL, rawURL, applyURL string) []string {
	keys := []string{"url:" + canonURL}
	if lk := listing.LegacyKey(rawURL); lk != "" {
		keys = append(keys, "legacy:"+lk)
	}
	if applyURL != "" {
		if lk := listing.LegacyKey(applyURL); lk != "" {
			keys = append(keys, "legacy:"+lk)
		}
		if ca, ok := source.ConcreteATS(applyURL); ok {
			keys = append(keys, "url:"+ca)
		}
	}
	return keys
}

func dropVacancy(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM vacancy_alias WHERE new_id=?`, id); err != nil {
		return fmt.Errorf("catalog: drop: aliases %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vacancy_fts WHERE rowid=?`, id); err != nil {
		return fmt.Errorf("catalog: drop: fts %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vacancy WHERE id=?`, id); err != nil {
		return fmt.Errorf("catalog: drop: vacancy %d: %w", id, err)
	}
	return nil
}

func (c *Catalog) Delete(ctx context.Context, id int64) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: delete: begin tx: %w", err)
	}
	defer tx.Rollback()

	var vacURL, applyURL string
	if err := tx.QueryRowContext(ctx,
		`SELECT url, apply_url FROM vacancy WHERE id=?`, id).Scan(&vacURL, &applyURL); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("catalog: delete: load: %w", err)
	}

	for _, k := range tombstoneKeys(vacURL, vacURL, applyURL) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tombstone (key) VALUES (?)`, k); err != nil {
			return fmt.Errorf("catalog: delete: tombstone: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO tombstone (key) SELECT 'url:'||url FROM vacancy_source WHERE vacancy_id=?`, id); err != nil {
		return fmt.Errorf("catalog: delete: tombstone sources: %w", err)
	}

	if err := dropVacancy(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

type Known struct {
	ID         int64
	Removed    bool
	New        bool
	HasText    bool
	HasCompany bool
	Requested  time.Time
}

func (c *Catalog) Known(ctx context.Context, p listing.Posting) (Known, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Known{}, fmt.Errorf("catalog: known: begin tx: %w", err)
	}
	defer tx.Rollback()

	r, err := resolve(ctx, tx, p)
	if err != nil {
		return Known{}, fmt.Errorf("catalog: known: %w", err)
	}
	if r.removed {
		return Known{Removed: true}, nil
	}
	if len(r.matchIDs) == 0 {
		return Known{New: true}, nil
	}
	sort.Slice(r.matchIDs, func(i, j int) bool { return r.matchIDs[i] < r.matchIDs[j] })
	var textState, requestedAt, company string
	if err := tx.QueryRowContext(ctx,
		`SELECT text_state, detail_requested_at, company FROM vacancy WHERE id = ?`, r.matchIDs[0]).
		Scan(&textState, &requestedAt, &company); err != nil {
		return Known{}, fmt.Errorf("catalog: known: load: %w", err)
	}
	hasText := textState == string(listing.TextFull) || textState == string(listing.TextSummary)
	var requested time.Time
	if requestedAt != "" {
		requested, _ = time.Parse(time.RFC3339, requestedAt)
	}
	return Known{ID: r.matchIDs[0], HasText: hasText, HasCompany: company != "", Requested: requested}, nil
}

func (c *Catalog) TitleCompany(ctx context.Context, id int64) (string, string, error) {
	var title, company string
	err := c.db.QueryRowContext(ctx, `SELECT title, company FROM vacancy WHERE id = ?`, id).Scan(&title, &company)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("catalog: title and company: %w", err)
	}
	return title, company, nil
}

func (c *Catalog) MarkRequested(ctx context.Context, id int64, at time.Time) error {
	_, err := c.db.ExecContext(ctx,
		`UPDATE vacancy SET detail_requested_at=? WHERE id=?`, at.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("catalog: mark requested: %w", err)
	}
	return nil
}

func anyTombstoned(ctx context.Context, tx *sql.Tx, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	ph := strings.Repeat("?,", len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM tombstone WHERE key IN (`+ph[:len(ph)-1]+`)`,
		args...).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func performMerge(ctx context.Context, tx *sql.Tx, matchIDs []int64) error {
	rows := make([]vacancyRow, len(matchIDs))
	for i, id := range matchIDs {
		r, err := loadVacancy(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("load vacancy %d: %w", id, err)
		}
		rows[i] = r
	}

	upd := rows[0]
	textChanged := false

	textRank := func(s string) int {
		switch listing.TextState(s) {
		case listing.TextFull:
			return 2
		case listing.TextSummary:
			return 1
		}
		return 0
	}

	for _, r := range rows[1:] {
		upd.firstSeen = pickEarlier(upd.firstSeen, r.firstSeen)
		upd.seenAt = pickEarlier(upd.seenAt, r.seenAt)
		if r.publishedAt != "" && (upd.publishedAt == "" || r.publishedAt < upd.publishedAt) {
			upd.publishedAt = r.publishedAt
			upd.publishedBasis = r.publishedBasis
		}
		if upd.applyURL == "" && r.applyURL != "" {
			upd.applyURL = r.applyURL
		}
		if textRank(r.textState) > textRank(upd.textState) {
			upd.textState = r.textState
			upd.contentHash = r.contentHash
			upd.body = r.body
			upd.version++
			textChanged = true
		}
	}

	ph := strings.Repeat("?,", len(matchIDs))
	mergeArgs := make([]any, len(matchIDs)+1)
	mergeArgs[0] = upd.id
	for i, id := range matchIDs {
		mergeArgs[i+1] = id
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO decision (profile, vacancy, stage, reason, decided_at)
		SELECT profile, ?, stage, reason, decided_at FROM (
			SELECT *, row_number() OVER (PARTITION BY profile ORDER BY
				CASE stage WHEN 'applied' THEN 3 WHEN 'shortlist' THEN 2 ELSE 1 END DESC,
				decided_at DESC) AS rn
			FROM decision WHERE vacancy IN (`+ph[:len(ph)-1]+`)) WHERE rn=1`,
		mergeArgs...); err != nil {
		return fmt.Errorf("merge decisions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE application SET vacancy=? WHERE vacancy IN (`+ph[:len(ph)-1]+`)`,
		mergeArgs...); err != nil {
		return fmt.Errorf("move applications: %w", err)
	}

	if _, err := save(ctx, tx, upd); err != nil {
		return fmt.Errorf("update kept %d: %w", upd.id, err)
	}

	for _, r := range rows[1:] {
		oldID := r.id
		if _, err := tx.ExecContext(ctx,
			`UPDATE vacancy_source SET vacancy_id = ? WHERE vacancy_id = ?`,
			upd.id, oldID); err != nil {
			return fmt.Errorf("move sources from %d: %w", oldID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE vacancy_alias SET new_id = ? WHERE new_id = ?`,
			upd.id, oldID); err != nil {
			return fmt.Errorf("repoint aliases from %d: %w", oldID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vacancy_alias (old_id, new_id) VALUES (?, ?)`,
			oldID, upd.id); err != nil {
			return fmt.Errorf("insert alias for %d: %w", oldID, err)
		}
		if err := dropVacancy(ctx, tx, oldID); err != nil {
			return fmt.Errorf("drop merged %d: %w", oldID, err)
		}
	}
	if textChanged {
		if err := WriteFTS(ctx, tx, upd.id, upd.title, upd.tagsJSON, upd.body); err != nil {
			return fmt.Errorf("fts kept %d: %w", upd.id, err)
		}
	}
	return nil
}

type VacancyText struct {
	ID, Version                                    int64
	Title, Company, Location, TagsJSON, FieldsJSON string
	TextState                                      string
	Body                                           []byte
	ReadingRecord, ReaderVersion, ReadingCreated   string
}

func (c *Catalog) VacancyTexts(ctx context.Context, after int64, limit int) ([]VacancyText, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT v.id, v.version, v.title, v.company, v.location, v.tags, v.source_fields, v.text_state, v.body,
		        COALESCE(r.record, ''), COALESCE(r.reader_version, ''), COALESCE(r.created, '')
		 FROM vacancy v
		 LEFT JOIN vacancy_reading r ON r.vacancy_id=v.id AND r.version=v.version
		 WHERE v.id>? ORDER BY v.id LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("catalog: vacancy texts: %w", err)
	}
	defer rows.Close()
	var out []VacancyText
	for rows.Next() {
		var r VacancyText
		if err := rows.Scan(&r.ID, &r.Version, &r.Title, &r.Company, &r.Location, &r.TagsJSON, &r.FieldsJSON,
			&r.TextState, &r.Body, &r.ReadingRecord, &r.ReaderVersion, &r.ReadingCreated); err != nil {
			return nil, fmt.Errorf("catalog: vacancy texts: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func pickEarlier(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" || b < a {
		return b
	}
	return a
}
