package shortlistcheck

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
)

type Stored struct {
	Digest string
	Check  Check
	Made   time.Time
}

func Digest(ctx context.Context, database *db.DB, profileID, vacancyID int64) (string, error) {
	var version int64
	var factsKey string
	err := database.QueryRowContext(ctx,
		`SELECT v.version, `+facts.KeySQLFor("site_fields", "conditions", "requirement_lines", "offer")+` FROM vacancy v WHERE v.id=?`,
		vacancyID).Scan(&version, &factsKey)
	if err != nil {
		return "", fmt.Errorf("shortlistcheck: digest: vacancy: %w", err)
	}
	var me string
	if err := database.QueryRowContext(ctx, `SELECT data FROM me WHERE id=1`).Scan(&me); err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("shortlistcheck: digest: me: %w", err)
	}
	pd, err := candidate.EffectiveProfile(ctx, database, profileID)
	if err != nil {
		return "", err
	}
	profile, _ := json.Marshal(pd)
	signature, err := candidate.EvidenceSignature(ctx, database)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\x00%s", version, factsKey, me, profile, signature, checkerVersion)
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func Load(ctx context.Context, database *db.DB, profileID, vacancyID int64) (Stored, bool, error) {
	var s Stored
	var result, made string
	err := database.QueryRowContext(ctx,
		`SELECT digest, result, created_at FROM shortlist_check WHERE profile=? AND vacancy=?`,
		profileID, vacancyID).Scan(&s.Digest, &result, &made)
	if err == sql.ErrNoRows {
		return Stored{}, false, nil
	}
	if err != nil {
		return Stored{}, false, fmt.Errorf("shortlistcheck: load: %w", err)
	}
	if err := json.Unmarshal([]byte(result), &s.Check); err != nil {
		return Stored{}, false, fmt.Errorf("shortlistcheck: decode: %w", err)
	}
	s.Made, _ = time.Parse(time.RFC3339, made)
	return s, true, nil
}

func Save(ctx context.Context, database *db.DB, profileID, vacancyID int64, digest string, c Check, now time.Time) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("shortlistcheck: save: %w", err)
	}
	_, err = database.ExecContext(ctx,
		`INSERT OR REPLACE INTO shortlist_check (profile, vacancy, digest, result, created_at) VALUES (?, ?, ?, ?, ?)`,
		profileID, vacancyID, digest, string(b), now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("shortlistcheck: save %d: %w", vacancyID, err)
	}
	return nil
}

const Kind = "shortlist_check"

func Subject(profileID, vacancyID int64) string { return fmt.Sprintf("%d/%d", profileID, vacancyID) }

func ParseSubject(s string) (profileID, vacancyID int64, err error) {
	if _, err = fmt.Sscanf(s, "%d/%d", &profileID, &vacancyID); err != nil {
		return 0, 0, fmt.Errorf("shortlistcheck: bad subject %q: %w", s, err)
	}
	return profileID, vacancyID, nil
}
