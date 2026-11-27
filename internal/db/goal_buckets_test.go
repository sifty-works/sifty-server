package db_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
)

func TestMigrationConvertsStoredGoalsIntoBuckets(t *testing.T) {
	before := fstest.MapFS{}
	names, err := fs.Glob(db.Migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "026" {
			break
		}
		data, err := fs.ReadFile(db.Migrations, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path, before)
	if err != nil {
		t.Fatalf("open before: %v", err)
	}
	profiles := []string{
		`{"name":"First","targetRoles":["Engineer"],"workModes":["remote"],"engagement":["contract"],"hours":{"mode":"part_time","weekly":20},"preferredLocations":[{"country":"DE","city":"Berlin"},{"country":"EU"}]}`,
		`{"name":"Second","targetRoles":["Designer"]}`,
		`{"name":"Third","workModes":["hybrid","onsite"],"hours":{"mode":"full_time"}}`,
		`{"name":"Fourth","workModes":["remote"],"relocation":"yes"}`,
	}
	if _, err := d.Exec(`UPDATE profile SET data=? WHERE id=1`, profiles[0]); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles[1:] {
		if _, err := d.Exec(`INSERT INTO profile (data) VALUES (?)`, p); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	d, err = db.Open(path, db.Migrations)
	if err != nil {
		t.Fatalf("open after: %v", err)
	}
	defer d.Close()

	weekly := 20.0
	want := []candidate.ProfileData{
		{Name: "First", TargetRoles: []string{"Engineer"},
			WorkMode:       candidate.Buckets{Preferred: []string{"remote"}, Never: []string{"hybrid", "onsite"}},
			EngagementType: candidate.Buckets{Preferred: []string{"contract"}, Unwanted: []string{"employment", "internship"}},
			WorkingTime:    candidate.Buckets{Preferred: []string{"part_time"}, Unwanted: []string{"full_time"}},
			Hours:          &candidate.Hours{Weekly: &weekly},
			Locations:      candidate.PlaceBuckets{Preferred: []candidate.Location{{Country: "DE", City: "Berlin"}, {Country: "EU"}}}},
		{Name: "Second", TargetRoles: []string{"Designer"}},
		{Name: "Third",
			WorkMode:    candidate.Buckets{Preferred: []string{"hybrid", "onsite"}, Never: []string{"remote"}},
			WorkingTime: candidate.Buckets{Preferred: []string{"full_time"}, Unwanted: []string{"part_time"}}},
		{Name: "Fourth", Relocation: "yes", WorkMode: candidate.Buckets{Preferred: []string{"remote"}, Unwanted: []string{"hybrid", "onsite"}}},
	}
	for i, w := range want {
		got, err := candidate.LoadProfileData(context.Background(), d, int64(i+1))
		if err != nil {
			t.Fatalf("profile %d: %v", i+1, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("profile %d = %+v, want %+v", i+1, got, w)
		}
		var raw string
		d.QueryRow(`SELECT data FROM profile WHERE id=?`, i+1).Scan(&raw)
		for _, old := range []string{`"workModes"`, `"engagement"`, `"preferredLocations"`, `"mode"`} {
			if strings.Contains(raw, old) {
				t.Errorf("profile %d still holds %s: %s", i+1, old, raw)
			}
		}
	}
}
