package importv1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/source"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const (
	oldDBFile     = "store.sqlite"
	oldConfigFile = "config/search.yaml"
	oldVersion    = 43
	tempDBName    = ".import-tmp.db"
)

type dropReason struct {
	ID     int64
	Reason string
}

type dupPair struct {
	KeptID    int64
	AliasedID int64
}

type importReport struct {
	VacanciesRead    int
	VacanciesWritten int
	VacanciesDropped []dropReason
	Duplicates       []dupPair

	SightingsRead    int
	SightingsWritten int
	SightingsSkipped int

	AliasesRead    int
	AliasesWritten int
	AliasesDropped []dropReason

	TombstonesRead    int
	TombstonesWritten int

	SourcesRead    int
	SourcesWritten int
	SourcesDropped []struct {
		Name   string
		Reason string
	}

	LastRunFound   bool
	LastRunWritten bool

	ExclusionsWritten  int
	ProfileSearchLines []string

	NonImportedKeys []string

	ProfilesRead    int
	ProfilesWritten int
	ProfilesDropped int

	TextlessDroppedSummary    int
	TextlessDroppedNotFetched int
	TextlessDroppedBlocked    int
	NoCompanyDropped          int

	StagesRead      int
	StagesNew       int
	StagesShortlist int
	StagesApplied   int
	StagesDismissed int
	StagesDropped   int

	StageDifferences []string

	ApplicationsRead    int
	ApplicationsWritten int

	ReadMarksBefore  int
	ReadMarksWritten int

	MeWritten bool

	ActiveProfileSet bool

	PhotoCopied  bool
	PhotoSkipped string

	QARead    int
	QAWritten int

	ReadingsWritten int

	DocumentsRead        int
	DocumentsWritten     int
	DocumentsDropped     map[string]int
	DocumentFilesMissing int
	DocumentFiles        []string

	ProposalsRead       int
	ProposalsWritten    int
	ProposalsDropped    map[string]int
	ProposalKeysDropped map[string]int

	LegacyFolded  []string
	LegacyDropped []string

	SettingsCarried []string
	Fit             fitComparison
	profileIDs      map[string]int64
	activeProfileID int64
}

type oldUser struct {
	id   string
	data string
}

type oldQA struct {
	id        string
	question  string
	answer    string
	createdAt string
	updatedAt string
}

type oldUserData struct {
	Identity struct {
		DisplayName string `json:"displayName"`
		LegalName   string `json:"legalName"`
		Headline    string `json:"headline"`
		Email       string `json:"email"`
		LinkedIn    string `json:"linkedin"`
		GitHub      string `json:"github"`
		Twitter     string `json:"twitter"`
	} `json:"identity"`
	Demographics struct {
		Gender    string   `json:"gender"`
		Race      []string `json:"race"`
		Ethnicity string   `json:"ethnicity"`
	} `json:"demographics"`
	Skills []struct {
		ID         string `json:"id"`
		Key        string `json:"key"`
		Name       string `json:"name"`
		Level      string `json:"level"`
		Provenance struct {
			Kind string `json:"kind"`
		} `json:"provenance"`
	} `json:"skills"`
	AbsentSkills []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"absentSkills"`
	ConfirmedFacts []struct {
		Key  string `json:"key"`
		Text string `json:"text"`
		Have bool   `json:"have"`
	} `json:"confirmedFacts"`
	Experience []struct {
		ID      string   `json:"id"`
		Company string   `json:"company"`
		Title   string   `json:"title"`
		Start   string   `json:"start"`
		End     string   `json:"end"`
		Summary string   `json:"summary"`
		Skills  []string `json:"skills"`
	} `json:"experience"`
	Languages []struct {
		Name  string `json:"name"`
		Level string `json:"level"`
	} `json:"languages"`
	Availability struct {
		NoticeValue  json.Number `json:"noticeValue"`
		NoticeUnit   string      `json:"noticeUnit"`
		NoticePeriod string      `json:"noticePeriod"`
		StartDate    string      `json:"startDate"`
		NoticeSet    bool        `json:"noticeSet"`
	} `json:"availability"`
	Geography struct {
		Current         string        `json:"current"`
		Eligibility     []string      `json:"eligibility"`
		Relocation      []interface{} `json:"relocation"`
		Sponsorship     string        `json:"sponsorship"`
		CurrentLocation *struct {
			Country string `json:"country"`
			City    string `json:"city"`
		} `json:"currentLocation"`
		WorkAuthorizations []struct {
			Country string `json:"country"`
			Status  string `json:"status"`
		} `json:"workAuthorizations"`
		CurrentLocationSet    bool `json:"currentLocationSet"`
		WorkAuthorizationsSet bool `json:"workAuthorizationsSet"`
	} `json:"geography"`
	OtherRequirements struct {
		Text string `json:"text"`
	} `json:"otherRequirements"`
	Dealbreakers      []interface{} `json:"dealbreakers"`
	LegacyPreferences *struct {
		Engagement struct {
			AllowFullTime bool     `json:"allow_full_time"`
			Preferred     []string `json:"preferred"`
		} `json:"engagement"`
		WorkArrangement struct {
			AllowHybrid bool     `json:"allow_hybrid"`
			AllowOnsite bool     `json:"allow_onsite"`
			Preferred   []string `json:"preferred"`
		} `json:"work_arrangement"`
	} `json:"legacyPreferences"`
	LegacyContextVersion string   `json:"legacyContextVersion"`
	TargetRoles          []string `json:"targetRoles"`
}

type oldProfileData struct {
	Compensation *struct {
		CashMin    *float64 `json:"cashMin"`
		CashTarget *float64 `json:"cashTarget"`
		Currency   string   `json:"currency"`
		GrossNet   string   `json:"grossNet"`
		Period     string   `json:"period"`
		DayRates   *struct {
			Min      *float64 `json:"min"`
			Target   *float64 `json:"target"`
			Currency string   `json:"currency"`
		} `json:"dayRates"`
		EquityOnTop *bool `json:"equityOnTop"`
	} `json:"compensation"`
	Engagement struct {
		Types []string `json:"types"`
	} `json:"engagement"`
	Hours *struct {
		Mode   string   `json:"mode"`
		Weekly *float64 `json:"weekly"`
	} `json:"hours"`
	WorkModes struct {
		Modes  []string      `json:"modes"`
		Limits []interface{} `json:"limits"`
	} `json:"workModes"`
	Geography struct {
		Current               string        `json:"current"`
		Desired               []interface{} `json:"desired"`
		Eligibility           []interface{} `json:"eligibility"`
		Relocation            []string      `json:"relocation"`
		WillingToRelocate     *bool         `json:"willingToRelocate"`
		Sponsorship           string        `json:"sponsorship"`
		CurrentLocation       interface{}   `json:"currentLocation"`
		PreferredLocations    []interface{} `json:"preferredLocations"`
		WorkAuthorizations    []interface{} `json:"workAuthorizations"`
		TimezoneIDs           []string      `json:"timezoneIds"`
		CurrentLocationSet    bool          `json:"currentLocationSet"`
		PreferredLocationsSet bool          `json:"preferredLocationsSet"`
	} `json:"geography"`
	Availability struct {
		NoticePeriod string `json:"noticePeriod"`
		StartDate    string `json:"startDate"`
	} `json:"availability"`
	OtherRequirements struct {
		Text string `json:"text"`
	} `json:"otherRequirements"`
	Dealbreakers []interface{} `json:"dealbreakers"`
}

type oldJob struct {
	id                int64
	identity          string
	title             string
	company           string
	originalURL       string
	applyURL          sql.NullString
	description       string
	descriptionStatus string
	location          sql.NullString
	publishedAt       sql.NullString
	publishedBasis    sql.NullString
	expiresAt         sql.NullString
	firstSeen         string
	lastSeen          string
	tags              string
	descriptionHTML   sql.NullString
	status            string
	appliedAt         sql.NullString
	seenAt            sql.NullString
}

type oldProfile struct {
	id         string
	name       string
	headline   string
	data       string
	createdAt  string
	archivedAt sql.NullString
}

type oldStage struct {
	profileID     string
	jobID         int64
	status        string
	outcomeReason sql.NullString
	updatedAt     string
}

type oldSighting struct {
	jobID  int64
	source string
	url    string
	seen   string
}

type oldAlias struct {
	oldID int64
	newID int64
}

type oldConfig struct {
	Sites []struct {
		Name    string `yaml:"name"`
		URL     string `yaml:"url"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"sites"`
}

func Import(ctx context.Context, fromDir, dbPath, settingsPath string, out io.Writer) error {
	dataDir := filepath.Dir(dbPath)

	if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(dataDir)
		if err != nil {
			return fmt.Errorf("reading target folder: %w", err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("The target folder is not empty.")
		}
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("creating target folder: %w", err)
	}

	var importErr error
	defer func() {
		if importErr != nil {
			os.RemoveAll(dataDir)
		}
	}()

	importErr = runImport(ctx, fromDir, dbPath, settingsPath, dataDir, out)
	return importErr
}

func runImport(ctx context.Context, fromDir, dbPath, settingsPath, dataDir string, out io.Writer) error {
	oldDBPath := filepath.Join(fromDir, oldDBFile)
	oldDSN := "file:" + oldDBPath + "?mode=ro"
	oldDB, err := sql.Open("sqlite", oldDSN)
	if err != nil {
		return fmt.Errorf("opening old database: %w", err)
	}
	defer oldDB.Close()

	tempPath := filepath.Join(dataDir, tempDBName)
	if _, err := oldDB.ExecContext(ctx, "VACUUM INTO ?", tempPath); err != nil {
		return fmt.Errorf("copying old database: %w", err)
	}
	oldDB.Close()
	defer os.Remove(tempPath)

	copyDB, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return fmt.Errorf("opening database copy: %w", err)
	}
	defer copyDB.Close()

	var version int
	if err := copyDB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading database version: %w", err)
	}
	if version != oldVersion {
		return fmt.Errorf("Unknown database version %d; this import reads version 43.", version)
	}

	newDB, err := db.Open(dbPath, db.Migrations)
	if err != nil {
		return fmt.Errorf("opening new database: %w", err)
	}
	defer newDB.Close()

	store, _, err := config.Open(settingsPath)
	if err != nil {
		return fmt.Errorf("opening settings: %w", err)
	}

	oldCfg, nonImportedKeys, err := loadOldConfig(filepath.Join(fromDir, oldConfigFile))
	if err != nil {
		oldCfg = &oldConfig{}
		nonImportedKeys = nil
	}

	report, err := doImport(ctx, copyDB, newDB, store, oldCfg)
	if err != nil {
		return err
	}
	if err := importDocuments(ctx, copyDB, newDB, fromDir, dataDir, report); err != nil {
		return fmt.Errorf("importing documents: %w", err)
	}
	report.NonImportedKeys = nonImportedKeys

	if err := importRules(fromDir, store, report); err != nil {
		return fmt.Errorf("writing weights and rates: %w", err)
	}
	if err := importProviders(ctx, fromDir, newDB, store, dataDir, report); err != nil {
		return fmt.Errorf("writing provider settings: %w", err)
	}
	if err := compareFit(ctx, copyDB, newDB, store.Get(), report, dataDir); err != nil {
		return fmt.Errorf("comparing Fits you: %w", err)
	}

	if photoErr := importPhoto(fromDir, dataDir, report); photoErr != nil {
		report.PhotoSkipped = "error reading photo: " + photoErr.Error()
	}

	reportText := formatReport(report)
	fmt.Fprint(out, reportText)
	reportPath := filepath.Join(dataDir, "import-report.txt")
	if err := os.WriteFile(reportPath, []byte(reportText), 0600); err != nil {
		fmt.Fprintf(out, "warning: could not write report file: %v\n", err)
	}

	return nil
}

func doImport(ctx context.Context, copyDB *sql.DB, newDB *db.DB, store *config.Store, oldCfg *oldConfig) (*importReport, error) {
	report := &importReport{}

	jobs, err := readJobs(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading jobs: %w", err)
	}

	sightings, err := readSightings(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading sightings: %w", err)
	}

	aliases, err := readAliases(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading aliases: %w", err)
	}

	hashes, err := readDeletedHashes(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading deleted_listings: %w", err)
	}

	idSeq, err := readIDSequence(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading id_sequence: %w", err)
	}

	lastRunTime, err := readLastRun(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading collection_runs: %w", err)
	}
	report.LastRunFound = !lastRunTime.IsZero()

	for _, j := range jobs {
		if j.seenAt.Valid && j.seenAt.String != "" {
			report.ReadMarksBefore++
		}
	}

	profiles, err := readProfiles(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading profiles: %w", err)
	}

	userData, err := readUser(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading users: %w", err)
	}

	qaRows, err := readQA(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading profile_qa: %w", err)
	}
	report.QARead = len(qaRows)

	oldActiveProfileID := readActiveProfileID(ctx, copyDB)

	stages, err := readProfileStages(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading profile stages: %w", err)
	}
	report.StagesRead = len(stages)

	lastAppliedMove, err := readLastAppliedMove(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading last applied moves: %w", err)
	}

	sourceKeyByName := buildSourceKeyMap(oldCfg)

	seenSourceNames := make(map[string]bool)
	for _, s := range sightings {
		seenSourceNames[s.source] = true
	}

	report.VacanciesRead = len(jobs)

	canonMap := make(map[string]int64)
	type validJob struct {
		job      *oldJob
		canonURL string
	}
	var validJobs []validJob

	for i := range jobs {
		j := &jobs[i]
		canon, err := listing.Canonical(j.originalURL)
		if err != nil {
			report.VacanciesDropped = append(report.VacanciesDropped, dropReason{ID: j.id, Reason: "no canonical address"})
			continue
		}
		validJobs = append(validJobs, validJob{job: j, canonURL: canon})
	}

	sort.Slice(validJobs, func(i, k int) bool {
		return validJobs[i].job.id < validJobs[k].job.id
	})

	duplicateAlias := make(map[int64]int64)
	for _, vj := range validJobs {
		if keptID, found := canonMap[vj.canonURL]; found {
			duplicateAlias[vj.job.id] = keptID
			report.Duplicates = append(report.Duplicates, dupPair{KeptID: keptID, AliasedID: vj.job.id})
		} else {
			canonMap[vj.canonURL] = vj.job.id
		}
	}

	type mergeData struct {
		firstSeen      string
		publishedAt    string
		publishedBasis string
		lastSeen       string
		textState      listing.TextState
		body           []byte
		hash           string
		seenAt         string
	}
	mergedData := make(map[int64]*mergeData)

	for _, vj := range validJobs {
		if _, isAlias := duplicateAlias[vj.job.id]; isAlias {
			continue
		}
		firstSeen := normDateUTC(vj.job.firstSeen)
		if firstSeen == "" {
			return nil, fmt.Errorf("vacancy %d: first_seen cannot be parsed: %q", vj.job.id, vj.job.firstSeen)
		}
		lastSeen := normDateUTC(vj.job.lastSeen)
		if lastSeen == "" {
			return nil, fmt.Errorf("vacancy %d: last_seen cannot be parsed: %q", vj.job.id, vj.job.lastSeen)
		}
		ts, body, hash, err := encodeVacancy(vj.job)
		if err != nil {
			report.VacanciesDropped = append(report.VacanciesDropped, dropReason{ID: vj.job.id, Reason: "encode: " + err.Error()})
			delete(canonMap, vj.canonURL)
			continue
		}
		pub, pubBasis := parsePublishedAt(vj.job.publishedAt, vj.job.publishedBasis)
		seenAt := ""
		if vj.job.seenAt.Valid && vj.job.seenAt.String != "" {
			seenAt = normDateUTC(vj.job.seenAt.String)
		}
		mergedData[vj.job.id] = &mergeData{
			firstSeen:      firstSeen,
			publishedAt:    pub,
			publishedBasis: pubBasis,
			lastSeen:       lastSeen,
			textState:      ts,
			body:           body,
			hash:           hash,
			seenAt:         seenAt,
		}
	}

	for _, vj := range validJobs {
		keptID, isAlias := duplicateAlias[vj.job.id]
		if !isAlias {
			continue
		}
		kept, ok := mergedData[keptID]
		if !ok {
			continue
		}
		ts, body, hash, err := encodeVacancy(vj.job)
		if err != nil {
			continue
		}
		pub, pubBasis := parsePublishedAt(vj.job.publishedAt, vj.job.publishedBasis)
		fs := normDateUTC(vj.job.firstSeen)
		if kept.firstSeen == "" || (fs != "" && fs < kept.firstSeen) {
			kept.firstSeen = fs
		}
		if pub != "" && (kept.publishedAt == "" || pub < kept.publishedAt) {
			kept.publishedAt = pub
			kept.publishedBasis = pubBasis
		}
		ls := normDateUTC(vj.job.lastSeen)
		if ls > kept.lastSeen {
			kept.lastSeen = ls
		}
		if betterTextState(ts, kept.textState) {
			kept.textState = ts
			kept.body = body
			kept.hash = hash
		}
		if vj.job.seenAt.Valid && vj.job.seenAt.String != "" {
			if sa := normDateUTC(vj.job.seenAt.String); sa > kept.seenAt {
				kept.seenAt = sa
			}
		}
	}

	keepIDs := make(map[int64]bool)
	for _, vj := range validJobs {
		if _, isAlias := duplicateAlias[vj.job.id]; !isAlias {
			if _, ok := mergedData[vj.job.id]; ok {
				keepIDs[vj.job.id] = true
			}
		}
	}

	hasActivity := make(map[int64]bool)
	for _, s := range stages {
		if s.status == "new" {
			continue
		}
		jobID := s.jobID
		if keptID, isAlias := duplicateAlias[jobID]; isAlias {
			jobID = keptID
		}
		hasActivity[jobID] = true
	}
	for _, j := range jobs {
		if !j.appliedAt.Valid || j.appliedAt.String == "" {
			continue
		}
		jobID := j.id
		if keptID, isAlias := duplicateAlias[jobID]; isAlias {
			jobID = keptID
		}
		hasActivity[jobID] = true
	}
	for id := range keepIDs {
		md := mergedData[id]
		if md.textState == listing.TextFull {
			continue
		}
		if hasActivity[id] {
			continue
		}
		delete(keepIDs, id)
		switch md.textState {
		case listing.TextSummary:
			report.TextlessDroppedSummary++
		case listing.TextNotFetched:
			report.TextlessDroppedNotFetched++
		case listing.TextBlocked:
			report.TextlessDroppedBlocked++
		}
	}

	slugCompanyMap := make(map[string]string)
	for _, vj := range validJobs {
		if !keepIDs[vj.job.id] {
			continue
		}
		co := vj.job.company
		if strings.EqualFold(co, "Unknown") {
			co = ""
		}
		if co == "" {
			continue
		}
		slug := urlCompanySlug(vj.canonURL, vj.job.title, nil)
		if slug == "" {
			continue
		}
		norm := strings.ToLower(slug)
		if _, exists := slugCompanyMap[norm]; !exists {
			slugCompanyMap[norm] = co
		}
	}

	companyByID := make(map[int64]string)
	for _, vj := range validJobs {
		if !keepIDs[vj.job.id] {
			continue
		}
		company := vj.job.company
		if strings.EqualFold(company, "Unknown") {
			company = ""
		}
		if company == "" {
			company = companyFromCanonURL(vj.canonURL, vj.job.title, slugCompanyMap)
		}
		if company == "" {
			delete(keepIDs, vj.job.id)
			report.NoCompanyDropped++
			continue
		}
		companyByID[vj.job.id] = company
	}

	sightingsByJob := make(map[int64][]oldSighting)
	for _, s := range sightings {
		sightingsByJob[s.jobID] = append(sightingsByJob[s.jobID], s)
	}

	tx, err := newDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, vj := range validJobs {
		j := vj.job
		if !keepIDs[j.id] {
			continue
		}
		md := mergedData[j.id]
		company := companyByID[j.id]
		identity := listing.Identity(company, j.title)

		applyURL := ""
		if j.applyURL.Valid && j.applyURL.String != "" {
			if ca, ok := source.ConcreteATS(j.applyURL.String); ok {
				applyURL = ca
			}
		}

		location := ""
		if j.location.Valid {
			location = j.location.String
		}

		expiresAt := ""
		if j.expiresAt.Valid && j.expiresAt.String != "" {
			if t, err := parseOldDate(j.expiresAt.String); err == nil {
				expiresAt = t.UTC().Format(time.RFC3339)
			}
		}

		_, err := tx.ExecContext(ctx, `
			INSERT INTO vacancy
				(id, identity, title, company, url, apply_url, location, tags, source_fields,
				 published_at, published_basis, expires_at,
				 first_seen, last_seen, text_state, content_hash, version, body, seen_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			j.id, identity, j.title, company, vj.canonURL, applyURL, location,
			j.tags, "{}",
			md.publishedAt, md.publishedBasis, expiresAt,
			md.firstSeen, md.lastSeen, string(md.textState), md.hash, 1, md.body,
			md.seenAt,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting vacancy %d: %w", j.id, err)
		}
		if err := catalog.WriteFTS(ctx, tx, j.id, j.title, j.tags, md.body); err != nil {
			return nil, fmt.Errorf("fts for vacancy %d: %w", j.id, err)
		}
		report.VacanciesWritten++
		if md.seenAt != "" {
			report.ReadMarksWritten++
		}
	}

	report.SightingsRead = len(sightings)

	usedCanonURLs := make(map[string]bool)

	for _, vj := range validJobs {
		j := vj.job
		if !keepIDs[j.id] {
			continue
		}
		jobSightings := sightingsByJob[j.id]
		for _, dp := range report.Duplicates {
			if dp.KeptID == j.id {
				jobSightings = append(jobSightings, sightingsByJob[dp.AliasedID]...)
			}
		}

		ownSource := "name:import"
		ownSeenAt := normDateUTC(j.lastSeen)
		if len(jobSightings) > 0 {
			sort.Slice(jobSightings, func(a, b int) bool {
				return jobSightings[a].seen < jobSightings[b].seen
			})
			earliestSighting := jobSightings[0]
			ownSource = resolveSource(earliestSighting.source, sourceKeyByName)
		}

		if !usedCanonURLs[vj.canonURL] {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO vacancy_source (vacancy_id, source, url, seen_at) VALUES (?,?,?,?)`,
				j.id, ownSource, vj.canonURL, ownSeenAt,
			); err != nil {
				return nil, fmt.Errorf("inserting own-address sighting for %d: %w", j.id, err)
			}
			usedCanonURLs[vj.canonURL] = true
			report.SightingsWritten++
		}

		for _, s := range jobSightings {
			canonSightURL, err := listing.Canonical(s.url)
			if err != nil {
				report.SightingsSkipped++
				continue
			}
			if usedCanonURLs[canonSightURL] {
				report.SightingsSkipped++
				continue
			}
			srcKey := resolveSource(s.source, sourceKeyByName)
			seenAt := normDateUTC(s.seen)
			if seenAt == "" {
				seenAt = ownSeenAt
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO vacancy_source (vacancy_id, source, url, seen_at) VALUES (?,?,?,?)`,
				j.id, srcKey, canonSightURL, seenAt,
			); err != nil {
				return nil, fmt.Errorf("inserting sighting for %d: %w", j.id, err)
			}
			usedCanonURLs[canonSightURL] = true
			report.SightingsWritten++
		}
	}

	for _, dp := range report.Duplicates {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vacancy_alias (old_id, new_id) VALUES (?,?)`,
			dp.AliasedID, dp.KeptID,
		); err != nil {
			return nil, fmt.Errorf("inserting duplicate alias %d→%d: %w", dp.AliasedID, dp.KeptID, err)
		}
	}

	report.AliasesRead = len(aliases)
	aliasMap := make(map[int64]int64, len(aliases))
	for _, a := range aliases {
		aliasMap[a.oldID] = a.newID
	}

	for _, a := range aliases {
		target, reason := resolveAliasTarget(a.oldID, a.newID, aliasMap, keepIDs, 50)
		if reason != "" {
			report.AliasesDropped = append(report.AliasesDropped, dropReason{ID: a.oldID, Reason: reason})
			continue
		}
		if keepIDs[a.oldID] {
			report.AliasesDropped = append(report.AliasesDropped, dropReason{ID: a.oldID, Reason: "old_id is an imported vacancy"})
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO vacancy_alias (old_id, new_id) VALUES (?,?)`,
			a.oldID, target,
		); err != nil {
			return nil, fmt.Errorf("inserting alias %d→%d: %w", a.oldID, target, err)
		}
		report.AliasesWritten++
	}

	report.TombstonesRead = len(hashes)
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO tombstone (key) VALUES (?)`,
			"legacy:"+h,
		); err != nil {
			return nil, fmt.Errorf("inserting tombstone: %w", err)
		}
		report.TombstonesWritten++
	}

	var maxVacID, maxAliasID int64
	tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM vacancy`).Scan(&maxVacID)
	tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(old_id),0) FROM vacancy_alias`).Scan(&maxAliasID)
	maxID := maxVacID
	if maxAliasID > maxID {
		maxID = maxAliasID
	}
	if idSeq > maxID {
		maxID = idSeq
	}

	if maxID > 0 {
		res, err := tx.ExecContext(ctx,
			`UPDATE sqlite_sequence SET seq=? WHERE name='vacancy' AND seq<?`,
			maxID, maxID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating sqlite_sequence: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			var exists int
			tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_sequence WHERE name='vacancy'`).Scan(&exists)
			if exists == 0 {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO sqlite_sequence (name, seq) VALUES ('vacancy', ?)`, maxID,
				); err != nil {
					return nil, fmt.Errorf("inserting sqlite_sequence: %w", err)
				}
			}
		}
	}

	stopWords, err := readStopWords(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading stop_words: %w", err)
	}
	for _, sw := range stopWords {
		value := strings.TrimSpace(sw.word)
		if value == "" {
			continue
		}
		norm := strings.ToLower(value)
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO exclusion (kind,value,norm,profile_id,created) VALUES ('word',?,?,0,?)`,
			value, norm, sw.created)
		if err != nil {
			return nil, fmt.Errorf("inserting word exclusion %q: %w", value, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			report.ExclusionsWritten++
		}
	}
	companies, err := readBlockRules(ctx, copyDB)
	if err != nil {
		return nil, fmt.Errorf("reading block_rules: %w", err)
	}
	for _, c := range companies {
		value := strings.TrimSpace(c.value)
		if value == "" || strings.EqualFold(value, "Unknown") {
			continue
		}
		norm := strings.TrimSuffix(listing.Identity(value, ""), "|")
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO exclusion (kind,value,norm,profile_id,created) VALUES ('company',?,?,0,?)`,
			value, norm, c.created)
		if err != nil {
			return nil, fmt.Errorf("inserting company exclusion %q: %w", value, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			report.ExclusionsWritten++
		}
	}
	sort.Slice(profiles, func(i, k int) bool {
		if profiles[i].createdAt != profiles[k].createdAt {
			return profiles[i].createdAt < profiles[k].createdAt
		}
		return profiles[i].id < profiles[k].id
	})

	report.ProfilesRead = len(profiles)
	for _, p := range profiles {
		if p.archivedAt.Valid && p.archivedAt.String != "" {
			report.ProfilesDropped++
		}
	}

	profileIDMap := make(map[string]int64)
	report.profileIDs = profileIDMap
	type profileEntry struct {
		newID       int64
		name        string
		targetRoles []string
		keywords    []string
		allOf       []string
		except      []string
		profileData candidate.ProfileData
	}
	var profilesByNewID []profileEntry

	anyProfileHeadline := false
	for _, p := range profiles {
		if p.archivedAt.Valid && p.archivedAt.String != "" {
			continue
		}
		if strings.TrimSpace(p.headline) != "" {
			anyProfileHeadline = true
			break
		}
	}

	userHeadline := ""
	hasStructuredNotice := false
	if userData != nil {
		var ud oldUserData
		if json.Unmarshal([]byte(userData.data), &ud) == nil {
			userHeadline = strings.TrimSpace(ud.Identity.Headline)
			if ud.Availability.NoticeSet {
				if nv := ud.Availability.NoticeValue.String(); nv != "" && nv != "0" && nv != "<nil>" {
					if f, fErr := strconv.ParseFloat(nv, 64); fErr == nil && f > 0 {
						hasStructuredNotice = ud.Availability.NoticeUnit != ""
					}
				}
			}
		}
	}

	for _, p := range profiles {
		if p.archivedAt.Valid && p.archivedAt.String != "" {
			continue
		}
		newID := int64(len(profilesByNewID) + 1)
		name := p.name
		if name == "" {
			name = "Profile"
		}
		roles, keywords, allOf, except := parseOldProfileData(p.data)
		profileIDMap[p.id] = newID

		pd, legacyFolded, legacyDropped := convertOldProfileData(p.data, newID, hasStructuredNotice)
		pd.Name = name
		pd.TargetRoles = roles
		pd.Keywords = keywords
		pd.AllOf = allOf

		headline := strings.TrimSpace(p.headline)
		if headline == "" && newID == 1 && !anyProfileHeadline {
			headline = userHeadline
		}
		pd.Headline = headline

		report.LegacyFolded = append(report.LegacyFolded, legacyFolded...)
		report.LegacyDropped = append(report.LegacyDropped, legacyDropped...)

		profilesByNewID = append(profilesByNewID, profileEntry{
			newID: newID, name: name,
			targetRoles: roles, keywords: keywords, allOf: allOf, except: except,
			profileData: pd,
		})
		report.ProfilesWritten++
	}

	if len(profilesByNewID) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM profile`); err != nil {
			return nil, fmt.Errorf("deleting default profile: %w", err)
		}
		for i := range profilesByNewID {
			pp := &profilesByNewID[i]
			candidate.NormalizeProfileData(&pp.profileData)
			newData, _ := json.Marshal(pp.profileData)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO profile (id, data) VALUES (?,?)`,
				pp.newID, string(newData),
			); err != nil {
				return nil, fmt.Errorf("inserting profile %d: %w", pp.newID, err)
			}
		}
		maxProfileID := int64(len(profilesByNewID))
		res, err := tx.ExecContext(ctx,
			`UPDATE sqlite_sequence SET seq=? WHERE name='profile' AND seq<?`,
			maxProfileID, maxProfileID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating sqlite_sequence for profile: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_sequence WHERE name='profile'`).Scan(&exists)
			if exists == 0 {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO sqlite_sequence (name, seq) VALUES ('profile', ?)`, maxProfileID,
				); err != nil {
					return nil, fmt.Errorf("inserting sqlite_sequence for profile: %w", err)
				}
			}
		}

		for _, pp := range profilesByNewID {
			for _, w := range pp.except {
				value := strings.TrimSpace(w)
				if value == "" {
					continue
				}
				norm := strings.ToLower(value)
				res, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO exclusion (kind,value,norm,profile_id,created) VALUES ('word',?,?,?,?)`,
					value, norm, pp.newID, time.Now().UTC().Format(time.RFC3339))
				if err != nil {
					return nil, fmt.Errorf("inserting profile %d except word %q: %w", pp.newID, value, err)
				}
				if n, _ := res.RowsAffected(); n > 0 {
					report.ExclusionsWritten++
				}
			}
		}

		for _, pp := range profilesByNewID {
			report.ProfileSearchLines = append(report.ProfileSearchLines,
				buildProfileSearchLine(pp.newID, pp.name, pp.targetRoles, pp.keywords, pp.allOf, pp.except))
		}
	}

	type decisionEntry struct {
		stage     string
		reason    string
		decidedAt string
	}
	stageMap := make(map[int64]map[int64]*decisionEntry)

	for _, s := range stages {
		newProfileID, ok := profileIDMap[s.profileID]
		if !ok {
			report.StagesDropped++
			continue
		}
		newVacancyID := s.jobID
		if keptID, isAlias := duplicateAlias[newVacancyID]; isAlias {
			newVacancyID = keptID
		}
		if !keepIDs[newVacancyID] {
			report.StagesDropped++
			continue
		}
		var newStage, reason string
		switch s.status {
		case "new":
			report.StagesNew++
			continue
		case "saved":
			newStage = "shortlist"
			report.StagesShortlist++
		case "rejected":
			newStage = "dismissed"
			if s.outcomeReason.Valid {
				reason = s.outcomeReason.String
			}
			report.StagesDismissed++
		case "applied":
			newStage = "applied"
			report.StagesApplied++
		default:
			report.StagesDropped++
			continue
		}
		decidedAt := normDateUTC(s.updatedAt)
		if decidedAt == "" {
			decidedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if _, ok := stageMap[newProfileID]; !ok {
			stageMap[newProfileID] = make(map[int64]*decisionEntry)
		}
		existing, exists := stageMap[newProfileID][newVacancyID]
		if !exists || stageStrength(newStage) > stageStrength(existing.stage) ||
			(stageStrength(newStage) == stageStrength(existing.stage) && decidedAt > existing.decidedAt) {
			stageMap[newProfileID][newVacancyID] = &decisionEntry{
				stage:     newStage,
				reason:    reason,
				decidedAt: decidedAt,
			}
		}
	}

	for profileID, vacMap := range stageMap {
		for vacancyID, entry := range vacMap {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO decision (profile, vacancy, stage, reason, decided_at) VALUES (?,?,?,?,?)`,
				profileID, vacancyID, entry.stage, entry.reason, entry.decidedAt,
			); err != nil {
				return nil, fmt.Errorf("inserting decision profile=%d vacancy=%d: %w", profileID, vacancyID, err)
			}
		}
	}

	if len(profilesByNewID) > 0 {
		firstProfileNewID := profilesByNewID[0].newID
		firstProfileDecisions := stageMap[firstProfileNewID]
		for _, j := range jobs {
			if !keepIDs[j.id] {
				continue
			}
			var jobsStage string
			switch j.status {
			case "saved":
				jobsStage = "shortlist"
			case "rejected":
				jobsStage = "dismissed"
			case "applied":
				jobsStage = "applied"
			default:
				jobsStage = ""
			}
			var profileStage string
			if entry, ok := firstProfileDecisions[j.id]; ok {
				profileStage = entry.stage
			}
			if jobsStage != profileStage {
				report.StageDifferences = append(report.StageDifferences,
					fmt.Sprintf("vacancy id=%d: jobs.status=%q, first profile=%q", j.id, j.status, profileStage))
			}
		}
	}

	type appKey struct {
		vacancyID int64
		appliedAt string
	}
	seenApps := make(map[appKey]bool)

	for _, j := range jobs {
		if !j.appliedAt.Valid || j.appliedAt.String == "" {
			continue
		}
		appliedAt := normDateUTC(j.appliedAt.String)
		if appliedAt == "" {
			continue
		}
		report.ApplicationsRead++

		newVacancyID := j.id
		if keptID, isAlias := duplicateAlias[newVacancyID]; isAlias {
			newVacancyID = keptID
		}
		if !keepIDs[newVacancyID] {
			continue
		}

		key := appKey{newVacancyID, appliedAt}
		if seenApps[key] {
			continue
		}
		seenApps[key] = true

		var newProfileID int64
		var profileName string
		if oldPID, ok := lastAppliedMove[j.id]; ok {
			if newID, ok := profileIDMap[oldPID]; ok {
				newProfileID = newID
			}
		}
		if newProfileID == 0 && len(profilesByNewID) > 0 {
			newProfileID = profilesByNewID[0].newID
		}
		if newProfileID == 0 {
			newProfileID = 1
		}
		for _, pp := range profilesByNewID {
			if pp.newID == newProfileID {
				profileName = pp.name
				break
			}
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO application (vacancy, applied_at, profile, profile_name) VALUES (?,?,?,?)`,
			newVacancyID, appliedAt, newProfileID, profileName,
		); err != nil {
			return nil, fmt.Errorf("inserting application for vacancy=%d: %w", newVacancyID, err)
		}
		report.ApplicationsWritten++
	}

	if !lastRunTime.IsZero() {
		ts := lastRunTime.UTC().Format("2006-01-02T15:04:05Z")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
			VALUES ('collect', '', 0, 'done', 1, ?, '{}', '', NULL, ?, ?)`,
			ts, ts, ts,
		); err != nil {
			return nil, fmt.Errorf("inserting last run: %w", err)
		}
		report.LastRunWritten = true
	}

	if userData != nil {
		var ud oldUserData
		if json.Unmarshal([]byte(userData.data), &ud) == nil {
			me, myLegacyFolded, myLegacyDropped := convertOldUserData(ud)
			report.LegacyFolded = append(report.LegacyFolded, myLegacyFolded...)
			report.LegacyDropped = append(report.LegacyDropped, myLegacyDropped...)
			if len(profilesByNewID) > 0 && len(myLegacyFolded) > 0 {
				first := &profilesByNewID[0]
				for _, fe := range myDataFoldEntries(myLegacyFolded) {
					line := fe.label + " (previous version): " + fe.text
					first.profileData.OtherRequirements = appendLegacy(first.profileData.OtherRequirements, line)
				}
				newData, _ := json.Marshal(first.profileData)
				if _, err := tx.ExecContext(ctx, `UPDATE profile SET data=? WHERE id=?`, string(newData), first.newID); err != nil {
					return nil, fmt.Errorf("updating first profile with legacy text: %w", err)
				}
			}
			candidate.NormalizeMeData(&me)
			meJSON, _ := json.Marshal(me)
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO me (id, data) VALUES (1, ?)`, string(meJSON)); err != nil {
				return nil, fmt.Errorf("inserting me: %w", err)
			}
			report.MeWritten = true
		}
	}

	if oldActiveProfileID != "" {
		if newID, ok := profileIDMap[oldActiveProfileID]; ok {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile', ?)`,
				strconv.FormatInt(newID, 10)); err != nil {
				return nil, fmt.Errorf("setting active profile: %w", err)
			}
			report.ActiveProfileSet = true
			report.activeProfileID = newID
		}
	}

	for _, q := range qaRows {
		question := strings.TrimSpace(q.question)
		if question == "" {
			continue
		}
		createdAt := normDateUTC(q.createdAt)
		if createdAt == "" {
			createdAt = time.Now().UTC().Format(time.RFC3339)
		}
		updatedAt := normDateUTC(q.updatedAt)
		if updatedAt == "" {
			updatedAt = createdAt
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO qa (question, answer, created_at, updated_at) VALUES (?,?,?,?)`,
			question, strings.TrimSpace(q.answer), createdAt, updatedAt,
		); err != nil {
			return nil, fmt.Errorf("inserting qa: %w", err)
		}
		report.QAWritten++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	sourceSightings := buildSourceSightingSet(sightings)
	report.SourcesRead = len(oldCfg.Sites)

	seenSourceKeys := make(map[string]bool)
	var newSources []config.SourceConfig

	for _, site := range oldCfg.Sites {
		key, err := config.SourceKey(site.URL)
		if err != nil {
			report.SourcesDropped = append(report.SourcesDropped, struct {
				Name   string
				Reason string
			}{Name: site.Name, Reason: "invalid address: " + err.Error()})
			continue
		}
		if len([]rune(site.Name)) > 100 {
			report.SourcesDropped = append(report.SourcesDropped, struct {
				Name   string
				Reason string
			}{Name: site.Name, Reason: "name longer than 100 characters"})
			continue
		}
		if seenSourceKeys[key] {
			report.SourcesDropped = append(report.SourcesDropped, struct {
				Name   string
				Reason string
			}{Name: site.Name, Reason: "duplicate source key"})
			continue
		}
		if !sourceSightings[site.Name] {
			report.SourcesDropped = append(report.SourcesDropped, struct {
				Name   string
				Reason string
			}{Name: site.Name, Reason: "no vacancy"})
			continue
		}
		seenSourceKeys[key] = true
		newSources = append(newSources, config.SourceConfig{
			Name:    site.Name,
			URL:     site.URL,
			Enabled: site.Enabled,
		})
	}

	if err := store.Update(func(c *config.Config) error {
		c.Sources = newSources
		return nil
	}); err != nil {
		return nil, fmt.Errorf("writing sources: %w", err)
	}
	report.SourcesWritten = len(newSources)

	n, err := importReadings(ctx, copyDB, newDB)
	if err != nil {
		return nil, fmt.Errorf("importing readings: %w", err)
	}
	report.ReadingsWritten = n

	return report, nil
}

type oldExtRecord struct {
	Requirements []struct {
		Text         string   `json:"text"`
		Kind         string   `json:"kind"`
		Level        string   `json:"level"`
		Quote        string   `json:"quote"`
		Alternatives []string `json:"alternatives"`
	} `json:"requirements"`
	Conditions map[string]*struct {
		Value string `json:"value"`
		Quote string `json:"quote"`
	} `json:"conditions"`
}

func importReadings(ctx context.Context, oldDB *sql.DB, newDB *db.DB) (int, error) {
	var n int
	if err := oldDB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE name='match_vacancy_extractions'`).Scan(&n); err != nil || n == 0 {
		return 0, nil
	}
	rows, err := oldDB.QueryContext(ctx,
		`SELECT job_id, record, created_at FROM match_vacancy_extractions ORDER BY job_id, created_at ASC`)
	if err != nil {
		return 0, fmt.Errorf("reading extractions: %w", err)
	}
	defer rows.Close()
	written := 0
	for rows.Next() {
		var jobID int64
		var recJSON, createdAt string
		if err := rows.Scan(&jobID, &recJSON, &createdAt); err != nil {
			return written, fmt.Errorf("scanning extraction: %w", err)
		}
		var ext oldExtRecord
		if json.Unmarshal([]byte(recJSON), &ext) != nil {
			continue
		}
		var body []byte
		newDB.QueryRowContext(ctx, `SELECT body FROM vacancy WHERE id=?`, jobID).Scan(&body)
		plainText := ""
		if body != nil {
			if h, e := catalog.Body(body); e == nil {
				plainText = listing.PlainText(h)
			}
		}
		rec := reading.Record{}
		condFn := func(key string) *reading.Condition {
			if c := ext.Conditions[key]; c != nil && c.Value != "" {
				return &reading.Condition{Value: c.Value, Quote: c.Quote}
			}
			return nil
		}
		rec.Money = condFn("compensation")
		rec.WorkMode = condFn("workMode")
		rec.Engagement = condFn("engagement")
		rec.Location = condFn("location")
		rec.TimeZone = condFn("timezone")
		rec.Hours = condFn("hours")
		for _, req := range ext.Requirements {
			r := reading.Requirement{
				Text:  req.Text,
				Kind:  req.Kind,
				Level: req.Level,
				Alts:  req.Alternatives,
			}
			if req.Quote != "" {
				if q, ok := reading.LocateQuote(plainText, req.Quote); ok {
					r.Quote = q
				} else {
					r.Quote = reading.Quote{Text: req.Quote}
				}
			}
			rec.Requirements = append(rec.Requirements, r)
		}
		if rec.Money == nil && rec.WorkMode == nil && rec.Engagement == nil &&
			rec.Location == nil && rec.TimeZone == nil && rec.Hours == nil &&
			len(rec.Requirements) == 0 {
			continue
		}
		recBytes, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		created := normDateUTC(createdAt)
		if created == "" {
			created = createdAt
		}
		res, err := newDB.ExecContext(ctx,
			`INSERT OR REPLACE INTO vacancy_reading (vacancy_id, version, reader_version, record, created)
			SELECT ?, 1, 'import-1', ?, ? WHERE EXISTS (SELECT 1 FROM vacancy WHERE id=?)`,
			jobID, string(recBytes), created, jobID)
		if err != nil {
			return written, fmt.Errorf("inserting reading for vacancy %d: %w", jobID, err)
		}
		if k, _ := res.RowsAffected(); k > 0 {
			written++
		}
	}
	return written, rows.Err()
}

func importPhoto(fromDir, dataDir string, report *importReport) error {
	avatarMeta, err := os.ReadFile(filepath.Join(fromDir, "profile-avatar.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta struct {
		StoragePath string `json:"storagePath"`
		ContentType string `json:"contentType"`
		ByteSize    int64  `json:"byteSize"`
	}
	if err := json.Unmarshal(avatarMeta, &meta); err != nil {
		report.PhotoSkipped = "could not parse profile-avatar.json"
		return nil
	}
	if meta.StoragePath == "" {
		return nil
	}
	const maxSize = 2 << 20
	if meta.ByteSize > maxSize {
		report.PhotoSkipped = fmt.Sprintf("photo too large (%d bytes, limit 2 MiB)", meta.ByteSize)
		return nil
	}
	srcPath := filepath.Join(fromDir, meta.StoragePath)
	data, err := os.ReadFile(srcPath)
	if os.IsNotExist(err) {
		report.PhotoSkipped = "photo file not found at " + meta.StoragePath
		return nil
	}
	if err != nil {
		return err
	}
	if int64(len(data)) > maxSize {
		report.PhotoSkipped = fmt.Sprintf("photo too large (%d bytes, limit 2 MiB)", len(data))
		return nil
	}
	ct := http.DetectContentType(data)
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		report.PhotoSkipped = fmt.Sprintf("photo type %q is not png, jpeg or webp", ct)
		return nil
	}
	filesDir := filepath.Join(dataDir, "files")
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return err
	}
	destPath := filepath.Join(filesDir, "me_photo")
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	report.PhotoCopied = true
	_ = sum
	return nil
}

func resolveAliasTarget(oldID, nextID int64, aliasMap map[int64]int64, keepIDs map[int64]bool, maxDepth int) (int64, string) {
	visited := map[int64]bool{oldID: true}
	cur := nextID
	for i := 0; i < maxDepth; i++ {
		if visited[cur] {
			return 0, "loop in alias chain"
		}
		if keepIDs[cur] {
			return cur, ""
		}
		next, ok := aliasMap[cur]
		if !ok {
			return 0, "target not an imported vacancy"
		}
		visited[cur] = true
		cur = next
	}
	return 0, "alias chain too long"
}

func resolveSource(name string, sourceKeyByName map[string]string) string {
	if key, ok := sourceKeyByName[name]; ok {
		return key
	}
	return "name:" + name
}

func buildSourceKeyMap(cfg *oldConfig) map[string]string {
	m := make(map[string]string, len(cfg.Sites))
	for _, site := range cfg.Sites {
		key, err := config.SourceKey(site.URL)
		if err != nil {
			continue
		}
		m[site.Name] = key
	}
	return m
}

func buildSourceSightingSet(sightings []oldSighting) map[string]bool {
	m := make(map[string]bool)
	for _, s := range sightings {
		m[s.source] = true
	}
	return m
}

func encodeVacancy(j *oldJob) (listing.TextState, []byte, string, error) {
	var ts listing.TextState
	hasText := (j.descriptionHTML.Valid && strings.TrimSpace(j.descriptionHTML.String) != "") ||
		strings.TrimSpace(j.description) != ""
	switch {
	case strings.HasPrefix(j.descriptionStatus, "blocked"):
		ts = listing.TextBlocked
	case !hasText:
		ts = listing.TextNotFetched
	case strings.HasPrefix(j.descriptionStatus, "available") || strings.HasPrefix(j.descriptionStatus, "feed description"):
		ts = listing.TextFull
	default:
		ts = listing.TextSummary
	}

	var rawText string
	if j.descriptionHTML.Valid && j.descriptionHTML.String != "" {
		rawText = j.descriptionHTML.String
	} else {
		rawText = PlainToHTML(j.description)
	}

	if rawText == "" {
		return ts, []byte{}, "", nil
	}

	normalized := listing.Normalize(rawText)
	body, hash, err := catalog.Encode(normalized)
	if err != nil {
		return ts, nil, "", err
	}
	return ts, body, hash, nil
}

func betterTextState(candidate, current listing.TextState) bool {
	return textRank(candidate) > textRank(current)
}

func stageStrength(stage string) int {
	switch stage {
	case "applied":
		return 3
	case "shortlist":
		return 2
	case "dismissed":
		return 1
	}
	return 0
}

func textRank(s listing.TextState) int {
	switch s {
	case listing.TextFull:
		return 2
	case listing.TextSummary:
		return 1
	}
	return 0
}

func parsePublishedAt(pubAt, pubBasis sql.NullString) (string, string) {
	if !pubAt.Valid || pubAt.String == "" {
		return "", ""
	}
	t, err := parseOldDate(pubAt.String)
	if err != nil {
		return "", ""
	}
	if t.After(time.Now().UTC()) {
		return "", ""
	}
	pub := t.UTC().Format(time.RFC3339)
	basis := ""
	if pubBasis.Valid {
		basis = pubBasis.String
	}
	if basis != "datePosted" && basis != "pubDate" {
		basis = ""
	}
	return pub, basis
}

func normDateUTC(s string) string {
	if s == "" {
		return ""
	}
	t, err := parseOldDate(s)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseOldDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date %q", s)
}

type oldStopWord struct{ word, created string }
type oldBlockRule struct{ value, created string }

func readStopWords(ctx context.Context, db *sql.DB) ([]oldStopWord, error) {
	rows, err := db.QueryContext(ctx, `SELECT word, created_at FROM stop_words ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldStopWord
	for rows.Next() {
		var r oldStopWord
		if err := rows.Scan(&r.word, &r.created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readBlockRules(ctx context.Context, db *sql.DB) ([]oldBlockRule, error) {
	rows, err := db.QueryContext(ctx, `SELECT value, created_at FROM block_rules WHERE kind='company' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldBlockRule
	for rows.Next() {
		var r oldBlockRule
		if err := rows.Scan(&r.value, &r.created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readJobs(ctx context.Context, db *sql.DB) ([]oldJob, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, identity, title, company, original_url, apply_url,
		       description, description_status, location,
		       published_at, published_basis, expires_at,
		       first_seen, last_seen, tags, description_html,
		       status, applied_at, seen_at
		FROM jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []oldJob
	for rows.Next() {
		var j oldJob
		if err := rows.Scan(
			&j.id, &j.identity, &j.title, &j.company, &j.originalURL, &j.applyURL,
			&j.description, &j.descriptionStatus, &j.location,
			&j.publishedAt, &j.publishedBasis, &j.expiresAt,
			&j.firstSeen, &j.lastSeen, &j.tags, &j.descriptionHTML,
			&j.status, &j.appliedAt, &j.seenAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func readProfiles(ctx context.Context, db *sql.DB) ([]oldProfile, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, headline, data, created_at, archived_at FROM user_profiles ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldProfile
	for rows.Next() {
		var p oldProfile
		if err := rows.Scan(&p.id, &p.name, &p.headline, &p.data, &p.createdAt, &p.archivedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readUser(ctx context.Context, db *sql.DB) (*oldUser, error) {
	var u oldUser
	err := db.QueryRowContext(ctx, `SELECT id, data FROM users LIMIT 1`).Scan(&u.id, &u.data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func readQA(ctx context.Context, db *sql.DB) ([]oldQA, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, question, answer, created_at, updated_at FROM profile_qa ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldQA
	for rows.Next() {
		var q oldQA
		if err := rows.Scan(&q.id, &q.question, &q.answer, &q.createdAt, &q.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func readActiveProfileID(ctx context.Context, db *sql.DB) string {
	var v string
	db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key='active_profile_id'`).Scan(&v)
	return v
}

func readProfileStages(ctx context.Context, db *sql.DB) ([]oldStage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT profile_id, job_id, status, outcome_reason, updated_at FROM profile_job_stages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldStage
	for rows.Next() {
		var s oldStage
		if err := rows.Scan(&s.profileID, &s.jobID, &s.status, &s.outcomeReason, &s.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func readLastAppliedMove(ctx context.Context, db *sql.DB) (map[int64]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT job_id, profile_id FROM profile_workflow_history WHERE to_stage='applied' ORDER BY created ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]string)
	for rows.Next() {
		var jobID int64
		var profileID string
		if err := rows.Scan(&jobID, &profileID); err != nil {
			return nil, err
		}
		result[jobID] = profileID
	}
	return result, rows.Err()
}

func readSightings(ctx context.Context, db *sql.DB) ([]oldSighting, error) {
	rows, err := db.QueryContext(ctx, `SELECT job_id, source, url, seen FROM sightings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sightings []oldSighting
	for rows.Next() {
		var s oldSighting
		if err := rows.Scan(&s.jobID, &s.source, &s.url, &s.seen); err != nil {
			return nil, err
		}
		sightings = append(sightings, s)
	}
	return sightings, rows.Err()
}

func readAliases(ctx context.Context, db *sql.DB) ([]oldAlias, error) {
	rows, err := db.QueryContext(ctx, `SELECT old_id, new_id FROM job_aliases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var aliases []oldAlias
	for rows.Next() {
		var a oldAlias
		if err := rows.Scan(&a.oldID, &a.newID); err != nil {
			return nil, err
		}
		aliases = append(aliases, a)
	}
	return aliases, rows.Err()
}

func readDeletedHashes(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT hash FROM deleted_listings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}
	return hashes, rows.Err()
}

func readIDSequence(ctx context.Context, db *sql.DB) (int64, error) {
	var v int64
	err := db.QueryRowContext(ctx, `SELECT value FROM id_sequence WHERE name='jobs'`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v - 1, nil
}

func readLastRun(ctx context.Context, db *sql.DB) (time.Time, error) {
	var s sql.NullString
	err := db.QueryRowContext(ctx, `SELECT MAX(started) FROM collection_runs`).Scan(&s)
	if err != nil || !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	t, parseErr := parseOldDate(s.String)
	if parseErr != nil {
		return time.Time{}, nil
	}
	return t, nil
}

func loadOldConfig(path string) (*oldConfig, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &oldConfig{}, nil, nil
		}
		return nil, nil, err
	}

	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing search.yaml: %w", err)
	}

	var cfg oldConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing search.yaml sites: %w", err)
	}

	var nonImported []string
	for k := range raw {
		if k != "version" && k != "sites" {
			nonImported = append(nonImported, k)
		}
	}
	sort.Strings(nonImported)

	return &cfg, nonImported, nil
}

func parseOldProfileData(data string) (targetRoles, keywords, allOf, except []string) {
	var d struct {
		TargetRoles []string `json:"targetRoles"`
		Search      struct {
			Keywords    []string `json:"keywords"`
			Except      []string `json:"except"`
			Expressions []struct {
				Or  []string `json:"or"`
				And []string `json:"and"`
			} `json:"expressions"`
		} `json:"search"`
	}
	if err := json.Unmarshal([]byte(data), &d); err != nil {
		return nil, nil, nil, nil
	}
	targetRoles = filterEmpty(d.TargetRoles)
	except = filterEmpty(d.Search.Except)
	if len(d.Search.Expressions) > 0 {
		for _, expr := range d.Search.Expressions {
			keywords = append(keywords, filterEmpty(expr.Or)...)
			allOf = append(allOf, filterEmpty(expr.And)...)
		}
	} else {
		keywords = filterEmpty(d.Search.Keywords)
	}
	return
}

func filterEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildProfileSearchLine(newID int64, name string, roles, keywords, allOf, except []string) string {
	var parts []string
	if len(roles) > 0 {
		parts = append(parts, "roles=["+strings.Join(roles, ", ")+"]")
	}
	if len(keywords) > 0 {
		parts = append(parts, "keywords=["+strings.Join(keywords, ", ")+"]")
	}
	if len(allOf) > 0 {
		parts = append(parts, "allOf=["+strings.Join(allOf, ", ")+"]")
	}
	if len(except) > 0 {
		parts = append(parts, "except=["+strings.Join(except, ", ")+"]")
	}
	header := fmt.Sprintf("id=%d (%s)", newID, name)
	if len(parts) == 0 {
		return header + ": no search fields"
	}
	return header + ": " + strings.Join(parts, "; ")
}

func mapSkillLevel(old string) string {
	switch old {
	case "Production":
		return "Expert"
	case "Familiar":
		return "Proficient"
	case "Learning":
		return "Beginner"
	}
	return old
}

func convertOldUserData(ud oldUserData) (candidate.MeData, []string, []string) {
	var me candidate.MeData
	var folded, dropped []string

	me.DisplayName = strings.TrimSpace(ud.Identity.DisplayName)
	me.LegalName = strings.TrimSpace(ud.Identity.LegalName)
	me.Email = strings.TrimSpace(ud.Identity.Email)
	me.LinkedIn = strings.TrimSpace(ud.Identity.LinkedIn)
	me.GitHub = strings.TrimSpace(ud.Identity.GitHub)
	me.X = strings.TrimSpace(ud.Identity.Twitter)

	for _, s := range ud.Skills {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			name = strings.TrimSpace(s.Key)
		}
		if name == "" {
			continue
		}
		sk := candidate.Skill{
			Name:   name,
			Level:  mapSkillLevel(s.Level),
			Source: strings.TrimSpace(s.Provenance.Kind),
		}
		me.Skills = append(me.Skills, sk)
	}

	for _, a := range ud.AbsentSkills {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = strings.TrimSpace(a.Key)
		}
		if name == "" {
			continue
		}
		me.AbsentSkills = append(me.AbsentSkills, candidate.AbsentSkill{Name: name})
	}

	for _, f := range ud.ConfirmedFacts {
		text := strings.TrimSpace(f.Text)
		if text == "" && f.Key == "" {
			continue
		}
		if text == "" {
			text = strings.TrimSpace(f.Key)
		}
		me.ConfirmedFacts = append(me.ConfirmedFacts, candidate.ConfirmedFact{
			Text: text,
			Have: f.Have,
		})
	}

	for _, e := range ud.Experience {
		exp := candidate.Experience{
			Company: strings.TrimSpace(e.Company),
			Title:   strings.TrimSpace(e.Title),
			Start:   strings.TrimSpace(e.Start),
			Skills:  filterEmpty(e.Skills),
		}
		end := strings.TrimSpace(e.End)
		if end == "" {
			exp.Current = true
		} else {
			exp.End = end
		}
		summary := strings.TrimSpace(e.Summary)
		if summary != "" {
			exp.Summary = htmlToMarkdown(summary)
		}
		me.Experience = append(me.Experience, exp)
	}

	for _, l := range ud.Languages {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			continue
		}
		me.Languages = append(me.Languages, candidate.Language{
			Name:  name,
			Level: l.Level,
		})
	}

	if ud.Availability.NoticeSet {
		av := &candidate.Availability{}
		if nv := ud.Availability.NoticeValue.String(); nv != "" && nv != "0" && nv != "<nil>" {
			if f, err := strconv.ParseFloat(nv, 64); err == nil && f > 0 {
				iv := int(f)
				av.NoticeValue = &iv
				av.NoticeUnit = ud.Availability.NoticeUnit
			}
		}
		av.StartDate = strings.TrimSpace(ud.Availability.StartDate)
		me.Availability = av
	}

	if ud.Geography.CurrentLocationSet && ud.Geography.CurrentLocation != nil {
		me.CurrentLocation = &candidate.Location{
			Country: strings.TrimSpace(ud.Geography.CurrentLocation.Country),
			City:    strings.TrimSpace(ud.Geography.CurrentLocation.City),
		}
	}

	if ud.Geography.WorkAuthorizationsSet {
		for _, wa := range ud.Geography.WorkAuthorizations {
			country := strings.TrimSpace(wa.Country)
			if country == "" {
				continue
			}
			me.WorkAuthorizations = append(me.WorkAuthorizations, candidate.WorkAuthorization{
				Country: country,
				Status:  wa.Status,
			})
		}
	}

	g := strings.TrimSpace(ud.Demographics.Gender)
	e2 := strings.TrimSpace(ud.Demographics.Ethnicity)
	races := filterEmpty(ud.Demographics.Race)
	if g != "" || e2 != "" || len(races) > 0 {
		me.Demographics = &candidate.Demographics{
			Gender:    g,
			Race:      races,
			Ethnicity: e2,
		}
	}

	var toFold []foldEntry

	if noticeFreeText := strings.TrimSpace(ud.Availability.NoticePeriod); noticeFreeText != "" {
		if me.Availability != nil && me.Availability.NoticeValue != nil {
			dropped = append(dropped, "My data (notice period): dropped (structured value present)")
		} else {
			toFold = append(toFold, foldEntry{"notice period", noticeFreeText})
		}
	}

	current := strings.TrimSpace(ud.Geography.Current)
	if current != "" {
		if me.CurrentLocation != nil {
			dropped = append(dropped, "My data (geography current): dropped (structured value present)")
		} else {
			toFold = append(toFold, foldEntry{"geography current", current})
		}
	}
	sponsorship := strings.TrimSpace(ud.Geography.Sponsorship)
	if sponsorship != "" {
		toFold = append(toFold, foldEntry{"sponsorship", sponsorship})
	}
	if len(ud.Geography.Eligibility) > 0 {
		if me.WorkAuthorizations != nil {
			dropped = append(dropped, "My data (eligibility): dropped (structured value present)")
		} else {
			toFold = append(toFold, foldEntry{"eligibility", strings.Join(filterEmpty(ud.Geography.Eligibility), ", ")})
		}
	}
	if ud.LegacyPreferences != nil {
		if len(ud.LegacyPreferences.WorkArrangement.Preferred) > 0 {
			toFold = append(toFold, foldEntry{"work arrangement", strings.Join(filterEmpty(ud.LegacyPreferences.WorkArrangement.Preferred), ", ")})
		}
		if len(ud.LegacyPreferences.Engagement.Preferred) > 0 {
			toFold = append(toFold, foldEntry{"engagement preferred", strings.Join(filterEmpty(ud.LegacyPreferences.Engagement.Preferred), ", ")})
		}
	}
	otherText := strings.TrimSpace(ud.OtherRequirements.Text)
	if otherText != "" {
		toFold = append(toFold, foldEntry{"other requirements", otherText})
	}
	var dbItems []string
	for _, d := range ud.Dealbreakers {
		if s, ok := d.(string); ok && strings.TrimSpace(s) != "" {
			dbItems = append(dbItems, strings.TrimSpace(s))
		}
	}
	if len(dbItems) > 0 {
		toFold = append(toFold, foldEntry{"dealbreakers", strings.Join(dbItems, "; ")})
	}

	if roles := filterEmpty(ud.TargetRoles); len(roles) > 0 {
		dropped = append(dropped, "My data (targetRoles): not imported ("+strings.Join(roles, ", ")+")")
	}

	for _, fe := range toFold {
		folded = append(folded, fmt.Sprintf("My data (%s): %s", fe.label, fe.text))
	}

	return me, folded, dropped
}

func myDataFoldEntries(foldedLines []string) []foldEntry {
	var out []foldEntry
	for _, line := range foldedLines {
		const prefix = "My data ("
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := line[len(prefix):]
		paren := strings.Index(rest, "): ")
		if paren < 0 {
			continue
		}
		label := rest[:paren]
		text := rest[paren+3:]
		out = append(out, foldEntry{label, text})
	}
	return out
}

func convertOldProfileData(data string, newID int64, hasStructuredNotice bool) (candidate.ProfileData, []string, []string) {
	var pd candidate.ProfileData
	var dropped []string
	var toFold []foldEntry
	var opd oldProfileData
	if err := json.Unmarshal([]byte(data), &opd); err != nil {
		return pd, nil, dropped
	}

	if opd.Compensation != nil {
		c := opd.Compensation
		comp := &candidate.Compensation{
			CashMin:     c.CashMin,
			CashTarget:  c.CashTarget,
			Currency:    strings.TrimSpace(c.Currency),
			GrossNet:    strings.TrimSpace(c.GrossNet),
			Period:      strings.TrimSpace(c.Period),
			EquityOnTop: c.EquityOnTop,
		}
		if c.DayRates != nil {
			comp.DayRates = &candidate.DayRates{
				Min:      c.DayRates.Min,
				Target:   c.DayRates.Target,
				Currency: strings.TrimSpace(c.DayRates.Currency),
			}
		}
		pd.Compensation = comp
	}

	for _, t := range opd.Engagement.Types {
		t = strings.TrimSpace(t)
		if t != "" {
			pd.Engagement = append(pd.Engagement, t)
		}
	}

	if opd.Hours != nil {
		pd.Hours = &candidate.Hours{
			Mode:   strings.TrimSpace(opd.Hours.Mode),
			Weekly: opd.Hours.Weekly,
		}
	}

	for _, m := range opd.WorkModes.Modes {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if m == "office" {
			m = "onsite"
		}
		pd.WorkModes = append(pd.WorkModes, m)
	}

	var limitItems []string
	for _, l := range opd.WorkModes.Limits {
		if s, ok := l.(string); ok && strings.TrimSpace(s) != "" {
			limitItems = append(limitItems, strings.TrimSpace(s))
		}
	}
	if len(limitItems) > 0 {
		if len(pd.WorkModes) > 0 {
			dropped = append(dropped, fmt.Sprintf("profile %d (work-mode limits): dropped (structured value present)", newID))
		} else {
			toFold = append(toFold, foldEntry{"work-mode limits", strings.Join(limitItems, "; ")})
		}
	}

	if opd.Geography.PreferredLocationsSet {
		for _, loc := range opd.Geography.PreferredLocations {
			switch v := loc.(type) {
			case map[string]interface{}:
				country, _ := v["country"].(string)
				city, _ := v["city"].(string)
				country = strings.TrimSpace(country)
				if country != "" {
					pd.PreferredLocations = append(pd.PreferredLocations, candidate.Location{
						Country: country,
						City:    strings.TrimSpace(city),
					})
				}
			case string:
				s := strings.TrimSpace(v)
				if s != "" {
					pd.PreferredLocations = append(pd.PreferredLocations, candidate.Location{Country: s})
				}
			}
		}
	}

	if opd.Geography.WillingToRelocate != nil {
		if *opd.Geography.WillingToRelocate {
			pd.Relocation = "yes"
		} else {
			pd.Relocation = "no"
		}
	}

	current := strings.TrimSpace(opd.Geography.Current)
	if current != "" {
		if len(pd.PreferredLocations) > 0 {
			dropped = append(dropped, fmt.Sprintf("profile %d (geography current): dropped (structured value present)", newID))
		} else {
			toFold = append(toFold, foldEntry{"geography current", current})
		}
	}

	var desiredItems []string
	for _, d := range opd.Geography.Desired {
		if s, ok := d.(string); ok && strings.TrimSpace(s) != "" {
			desiredItems = append(desiredItems, strings.TrimSpace(s))
		}
	}
	if len(desiredItems) > 0 {
		if len(pd.PreferredLocations) > 0 {
			dropped = append(dropped, fmt.Sprintf("profile %d (geography desired): dropped (structured value present)", newID))
		} else {
			toFold = append(toFold, foldEntry{"geography desired", strings.Join(desiredItems, ", ")})
		}
	}

	var eligItems []string
	for _, e := range opd.Geography.Eligibility {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			eligItems = append(eligItems, strings.TrimSpace(s))
		}
	}
	if len(eligItems) > 0 {
		toFold = append(toFold, foldEntry{"eligibility", strings.Join(eligItems, ", ")})
	}

	if len(opd.Geography.Relocation) > 0 {
		toFold = append(toFold, foldEntry{"relocation", strings.Join(filterEmpty(opd.Geography.Relocation), ", ")})
	}

	sponsorship := strings.TrimSpace(opd.Geography.Sponsorship)
	if sponsorship != "" {
		toFold = append(toFold, foldEntry{"sponsorship", sponsorship})
	}

	if noticeFreeText := strings.TrimSpace(opd.Availability.NoticePeriod); noticeFreeText != "" {
		if hasStructuredNotice {
			dropped = append(dropped, fmt.Sprintf("profile %d (notice period): dropped (structured value present)", newID))
		} else {
			toFold = append(toFold, foldEntry{"notice period", noticeFreeText})
		}
	}

	pd.TimeZones = filterEmpty(opd.Geography.TimezoneIDs)

	otherText := strings.TrimSpace(opd.OtherRequirements.Text)
	if otherText != "" {
		pd.OtherRequirements = htmlToMarkdown(otherText)
	}

	var dbItems []string
	for _, d := range opd.Dealbreakers {
		if s, ok := d.(string); ok && strings.TrimSpace(s) != "" {
			dbItems = append(dbItems, strings.TrimSpace(s))
		}
	}
	if len(dbItems) > 0 {
		toFold = append(toFold, foldEntry{"dealbreakers", strings.Join(dbItems, "; ")})
	}

	var foldedLines []string
	for _, fe := range toFold {
		line := fe.label + " (previous version): " + fe.text
		pd.OtherRequirements = appendLegacy(pd.OtherRequirements, line)
		foldedLines = append(foldedLines, fmt.Sprintf("profile %d (%s): %s", newID, fe.label, fe.text))
	}

	return pd, foldedLines, dropped
}

type foldEntry struct {
	label string
	text  string
}

func appendLegacy(other, line string) string {
	other = strings.TrimSpace(other)
	if other == "" {
		return line
	}
	return other + "\n\n" + line
}

func companyFromCanonURL(canonURL, title string, slugMap map[string]string) string {
	slug := urlCompanySlug(canonURL, title, slugMap)
	if slug == "" {
		return ""
	}
	return slugToCompany(slug, slugMap)
}

func urlCompanySlug(canonURL, title string, slugMap map[string]string) string {
	u, err := url.Parse(canonURL)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })

	if len(segs) >= 3 && segs[0] == "companies" && segs[2] == "jobs" {
		slug := segs[1]
		if strings.HasPrefix(slug, "zzz-") {
			return ""
		}
		return stripGetroDisambig(slug)
	}
	if host == "weworkremotely.com" && len(segs) >= 2 && segs[0] == "remote-jobs" {
		return trimTitleSuffix(segs[1], title, slugMap)
	}
	if (host == "web3.career" || host == "web3.careers") && len(segs) == 2 {
		id := segs[1]
		allDigit := len(id) >= 5 && strings.TrimFunc(id, func(r rune) bool { return r >= '0' && r <= '9' }) == ""
		if allDigit {
			return trimTitlePrefix(segs[0], title)
		}
	}
	return ""
}

func slugToCompany(slug string, slugMap map[string]string) string {
	if slug == "" {
		return ""
	}
	norm := strings.ToLower(slug)
	if co, ok := slugMap[norm]; ok && co != "" {
		return co
	}
	return titleCaseSlug(slug)
}

func titleCaseSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func trimTitleSuffix(slug, title string, slugMap map[string]string) string {
	if slug == "" || strings.HasPrefix(slug, "zzz-") {
		return ""
	}
	slug = strings.ToLower(slug)
	if i := strings.LastIndexByte(slug, '-'); i > 0 {
		if tail := slug[i+1:]; tail != "" && strings.TrimFunc(tail, func(r rune) bool { return r >= '0' && r <= '9' }) == "" {
			slug = slug[:i]
		}
	}
	if ts := slugifyTitle(title); ts != "" {
		for i := 0; i < len(slug); i++ {
			if slug[i] != '-' || i == 0 {
				continue
			}
			if end := slug[i+1:]; end == ts || strings.HasPrefix(ts, end+"-") {
				return slug[:i]
			}
		}
	}
	best := ""
	for norm := range slugMap {
		if len(norm) > len(best) && strings.HasPrefix(slug, norm) {
			if rest := slug[len(norm):]; rest == "" || rest[0] == '-' {
				best = norm
			}
		}
	}
	return best
}

func trimTitlePrefix(slug, title string) string {
	if slug == "" || strings.HasPrefix(slug, "zzz-") {
		return ""
	}
	if title != "" {
		if ts := slugifyTitle(title); ts != "" && strings.HasPrefix(slug, ts+"-") {
			return slug[len(ts)+1:]
		}
	}
	return ""
}

func stripGetroDisambig(slug string) string {
	parts := strings.Split(slug, "-")
	n := len(parts)
	end := n
	if n >= 6 {
		lens := [5]int{8, 4, 4, 4, 12}
		ok := true
	outer:
		for i, l := range lens {
			seg := parts[n-5+i]
			if len(seg) != l {
				ok = false
				break
			}
			for _, c := range seg {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					ok = false
					break outer
				}
			}
		}
		if ok {
			end = n - 5
		}
	}
	if end > 1 {
		seg := parts[end-1]
		if seg != "" && strings.TrimFunc(seg, func(r rune) bool { return r >= '0' && r <= '9' }) == "" {
			end--
		}
	}
	if end == n || end <= 0 {
		return slug
	}
	return strings.Join(parts[:end], "-")
}

var accentFold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ā", "a", "ă", "a", "ą", "a",
	"ç", "c", "ć", "c", "č", "c",
	"ď", "d", "đ", "d",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ē", "e", "ė", "e", "ę", "e", "ě", "e",
	"ğ", "g",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ī", "i", "ı", "i",
	"ł", "l", "ĺ", "l", "ľ", "l",
	"ñ", "n", "ń", "n", "ň", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ō", "o", "ő", "o",
	"ŕ", "r", "ř", "r",
	"ś", "s", "š", "s", "ş", "s", "ș", "s",
	"ť", "t", "ţ", "t", "ț", "t",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ū", "u", "ů", "u", "ű", "u",
	"ý", "y", "ÿ", "y",
	"ź", "z", "ž", "z", "ż", "z",
	"ß", "ss", "æ", "ae", "œ", "oe",
)

func slugifyTitle(s string) string {
	s = accentFold.Replace(strings.ToLower(s))
	var b strings.Builder
	hyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			hyphen = false
		} else if b.Len() > 0 && !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func formatReport(r *importReport) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Vacancies: %d read, %d written, %d dropped\n",
		r.VacanciesRead, r.VacanciesWritten, len(r.VacanciesDropped))
	for _, d := range r.VacanciesDropped {
		fmt.Fprintf(&sb, "  dropped id=%d: %s\n", d.ID, d.Reason)
	}
	textlessTotal := r.TextlessDroppedSummary + r.TextlessDroppedNotFetched + r.TextlessDroppedBlocked
	if textlessTotal > 0 {
		fmt.Fprintf(&sb, "  dropped (no full text, no decision): %d (summary: %d, not_fetched: %d, blocked: %d)\n",
			textlessTotal, r.TextlessDroppedSummary, r.TextlessDroppedNotFetched, r.TextlessDroppedBlocked)
	}
	if r.NoCompanyDropped > 0 {
		fmt.Fprintf(&sb, "  dropped (no employer found): %d\n", r.NoCompanyDropped)
	}
	if len(r.Duplicates) > 0 {
		fmt.Fprintf(&sb, "  duplicate canonical address pairs (%d):\n", len(r.Duplicates))
		for _, dp := range r.Duplicates {
			fmt.Fprintf(&sb, "    kept=%d aliased=%d\n", dp.KeptID, dp.AliasedID)
		}
	}

	fmt.Fprintf(&sb, "Sightings: %d read, %d written, %d skipped\n",
		r.SightingsRead, r.SightingsWritten, r.SightingsSkipped)

	fmt.Fprintf(&sb, "Aliases: %d read, %d written, %d dropped\n",
		r.AliasesRead, r.AliasesWritten, len(r.AliasesDropped))
	for _, d := range r.AliasesDropped {
		fmt.Fprintf(&sb, "  dropped alias id=%d: %s\n", d.ID, d.Reason)
	}

	fmt.Fprintf(&sb, "Tombstones: %d read, %d written\n",
		r.TombstonesRead, r.TombstonesWritten)

	fmt.Fprintf(&sb, "Sources: %d read, %d written, %d dropped\n",
		r.SourcesRead, r.SourcesWritten, len(r.SourcesDropped))
	for _, d := range r.SourcesDropped {
		fmt.Fprintf(&sb, "  dropped %q: %s\n", d.Name, d.Reason)
	}

	if r.LastRunFound {
		if r.LastRunWritten {
			fmt.Fprintf(&sb, "Last run: written\n")
		} else {
			fmt.Fprintf(&sb, "Last run: found but not written\n")
		}
	} else {
		fmt.Fprintf(&sb, "Last run: none found\n")
	}

	fmt.Fprintf(&sb, "Exclusions: %d written\n", r.ExclusionsWritten)

	if len(r.ProfileSearchLines) > 0 {
		fmt.Fprintf(&sb, "Profile search:\n")
		for _, line := range r.ProfileSearchLines {
			fmt.Fprintf(&sb, "  %s\n", line)
		}
	}

	if len(r.NonImportedKeys) > 0 {
		fmt.Fprintf(&sb, "Keys in search.yaml not imported: %s\n",
			strings.Join(r.NonImportedKeys, ", "))
	}

	fmt.Fprintf(&sb, "Profiles: %d read, %d written, %d dropped (archived)\n",
		r.ProfilesRead, r.ProfilesWritten, r.ProfilesDropped)

	fmt.Fprintf(&sb, "Stages: %d read, %d new (inbox), %d shortlist, %d applied, %d dismissed, %d dropped\n",
		r.StagesRead, r.StagesNew, r.StagesShortlist, r.StagesApplied, r.StagesDismissed, r.StagesDropped)

	if len(r.StageDifferences) > 0 {
		fmt.Fprintf(&sb, "Stage differences (jobs.status vs first profile): %d\n", len(r.StageDifferences))
		for _, d := range r.StageDifferences {
			fmt.Fprintf(&sb, "  %s\n", d)
		}
	} else {
		fmt.Fprintf(&sb, "Stage differences (jobs.status vs first profile): 0\n")
	}

	fmt.Fprintf(&sb, "Applications: %d read, %d written\n",
		r.ApplicationsRead, r.ApplicationsWritten)

	fmt.Fprintf(&sb, "Read marks: %d before, %d written\n",
		r.ReadMarksBefore, r.ReadMarksWritten)

	if r.MeWritten {
		fmt.Fprintf(&sb, "My data: written\n")
	} else {
		fmt.Fprintf(&sb, "My data: not found\n")
	}

	if r.ActiveProfileSet {
		fmt.Fprintf(&sb, "Active profile: set\n")
	} else {
		fmt.Fprintf(&sb, "Active profile: not set\n")
	}

	if r.PhotoCopied {
		fmt.Fprintf(&sb, "Photo: copied\n")
	} else if r.PhotoSkipped != "" {
		fmt.Fprintf(&sb, "Photo: skipped (%s)\n", r.PhotoSkipped)
	} else {
		fmt.Fprintf(&sb, "Photo: none\n")
	}

	fmt.Fprintf(&sb, "Questions & Answers: %d read, %d written\n", r.QARead, r.QAWritten)

	fmt.Fprintf(&sb, "Readings: %d written\n", r.ReadingsWritten)

	formatDocuments(&sb, r)
	formatProposals(&sb, r)

	if len(r.LegacyFolded) > 0 {
		fmt.Fprintf(&sb, "Legacy values folded into Other requirements (%d):\n", len(r.LegacyFolded))
		for _, line := range r.LegacyFolded {
			fmt.Fprintf(&sb, "  folded: %s\n", line)
		}
	}
	if len(r.LegacyDropped) > 0 {
		fmt.Fprintf(&sb, "Legacy values dropped (%d):\n", len(r.LegacyDropped))
		for _, line := range r.LegacyDropped {
			fmt.Fprintf(&sb, "  dropped: %s\n", line)
		}
	}
	if len(r.SettingsCarried) > 0 {
		fmt.Fprintf(&sb, "Settings that differ from the defaults (%d):\n", len(r.SettingsCarried))
		for _, line := range r.SettingsCarried {
			fmt.Fprintf(&sb, "  %s\n", line)
		}
	}
	formatFitComparison(&sb, r.Fit)

	return sb.String()
}

func PlainToHTML(plain string) string {
	if plain == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(plain, "\r\n", "\n"), "\n")

	type block struct {
		tag   string
		lines []string
	}

	var blocks []block
	var cur *block

	flush := func() {
		if cur != nil && len(cur.lines) > 0 {
			blocks = append(blocks, *cur)
		}
		cur = nil
	}

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			flush()
			continue
		}
		isBullet := strings.HasPrefix(stripped, "- ") ||
			strings.HasPrefix(stripped, "* ") ||
			strings.HasPrefix(stripped, "• ")
		if isBullet {
			if cur == nil || cur.tag != "ul" {
				flush()
				cur = &block{tag: "ul"}
			}
			text := strings.TrimPrefix(stripped, "- ")
			text = strings.TrimPrefix(text, "* ")
			text = strings.TrimPrefix(text, "• ")
			cur.lines = append(cur.lines, strings.TrimSpace(text))
		} else {
			if cur == nil {
				cur = &block{}
			} else if cur.tag != "" {
				flush()
				cur = &block{}
			}
			if len(cur.lines) > 0 {
				cur.lines[len(cur.lines)-1] += "\n" + stripped
			} else {
				cur.lines = append(cur.lines, stripped)
			}
		}
	}
	flush()

	var buf strings.Builder
	for _, b := range blocks {
		switch b.tag {
		case "ul":
			buf.WriteString("<ul>")
			for _, item := range b.lines {
				buf.WriteString("<li>")
				buf.WriteString(html.EscapeString(item))
				buf.WriteString("</li>")
			}
			buf.WriteString("</ul>")
		default:
			for _, para := range b.lines {
				buf.WriteString("<p>")
				buf.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>"))
				buf.WriteString("</p>")
			}
		}
	}
	return buf.String()
}
