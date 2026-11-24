package importv1_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
)

func insertUserRow(t *testing.T, fromDir string, data string) {
	t.Helper()
	odb := openOldDB(t, fromDir)
	_, err := odb.Exec(
		`INSERT INTO users (id, revision, data, created_at, updated_at)
		 VALUES ('u1', 1, ?, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
		data)
	if err != nil {
		t.Fatalf("insertUserRow: %v", err)
	}
}

func insertQARow(t *testing.T, fromDir, id, question, answer, createdAt string) {
	t.Helper()
	odb := openOldDB(t, fromDir)
	_, err := odb.Exec(
		`INSERT INTO profile_qa (id, user_id, question, answer, created_at, updated_at)
		 VALUES (?, 'u1', ?, ?, ?, ?)`,
		id, question, answer, createdAt, createdAt)
	if err != nil {
		t.Fatalf("insertQARow: %v", err)
	}
}

func setActiveProfileSetting(t *testing.T, fromDir, profileID string) {
	t.Helper()
	odb := openOldDB(t, fromDir)
	_, err := odb.Exec(
		`INSERT OR REPLACE INTO app_settings (key, value) VALUES ('active_profile_id', ?)`,
		profileID)
	if err != nil {
		t.Fatalf("setActiveProfileSetting: %v", err)
	}
}

func readMeData(t *testing.T, targetDir string) candidate.MeData {
	t.Helper()
	newDB := openNewDB(t, targetDir)
	var dataJSON string
	err := newDB.QueryRow(`SELECT data FROM me WHERE id=1`).Scan(&dataJSON)
	if err != nil {
		t.Fatalf("readMeData: %v", err)
	}
	var me candidate.MeData
	if err := json.Unmarshal([]byte(dataJSON), &me); err != nil {
		t.Fatalf("readMeData unmarshal: %v", err)
	}
	return me
}

func readProfileData(t *testing.T, targetDir string, id int64) candidate.ProfileData {
	t.Helper()
	newDB := openNewDB(t, targetDir)
	var dataJSON string
	err := newDB.QueryRow(`SELECT data FROM profile WHERE id=?`, id).Scan(&dataJSON)
	if err != nil {
		t.Fatalf("readProfileData(%d): %v", id, err)
	}
	var pd candidate.ProfileData
	if err := json.Unmarshal([]byte(dataJSON), &pd); err != nil {
		t.Fatalf("readProfileData unmarshal: %v", err)
	}
	return pd
}

func TestImport_MyDataIdentityFields(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"identity": {
			"displayName": "Alex Peron",
			"legalName": "Alexander Peron",
			"email": "alex@example.com",
			"linkedin": "https://linkedin.com/in/alexperon",
			"github": "alexperon",
			"twitter": "alexperon_x"
		}
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if me.DisplayName != "Alex Peron" {
		t.Errorf("DisplayName: got %q", me.DisplayName)
	}
	if me.LegalName != "Alexander Peron" {
		t.Errorf("LegalName: got %q", me.LegalName)
	}
	if me.Email != "alex@example.com" {
		t.Errorf("Email: got %q", me.Email)
	}
	if me.LinkedIn != "https://linkedin.com/in/alexperon" {
		t.Errorf("LinkedIn: got %q", me.LinkedIn)
	}
	if me.GitHub != "alexperon" {
		t.Errorf("GitHub: got %q", me.GitHub)
	}
	if me.X != "alexperon_x" {
		t.Errorf("X (twitter): got %q", me.X)
	}
	if !strings.Contains(report, "My data: written") {
		t.Errorf("report missing My data line: %s", report)
	}
}

func TestImport_SkillLevelMapping(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"skills": [
			{"name":"Go","level":"Production","provenance":{"kind":"manual"}},
			{"name":"Python","level":"Familiar","provenance":{"kind":"document"}},
			{"name":"Rust","level":"Learning","provenance":{"kind":"manual"}},
			{"name":"Java","level":"Expert","provenance":{"kind":"manual"}}
		]
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if len(me.Skills) != 4 {
		t.Fatalf("skills count: got %d, want 4", len(me.Skills))
	}
	wantLevels := map[string]string{
		"Go":     "Expert",
		"Python": "Proficient",
		"Rust":   "Beginner",
		"Java":   "Expert",
	}
	for _, sk := range me.Skills {
		want, ok := wantLevels[sk.Name]
		if !ok {
			t.Errorf("unexpected skill %q", sk.Name)
			continue
		}
		if sk.Level != want {
			t.Errorf("skill %q level: got %q, want %q", sk.Name, sk.Level, want)
		}
	}
	for _, sk := range me.Skills {
		if sk.Source == "" {
			t.Errorf("skill %q source: empty, want provenance kind", sk.Name)
		}
	}
}

func TestImport_ExperienceCurrentRole(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"experience": [
			{"id":"e1","company":"Acme","title":"Engineer","start":"2022-03","end":""},
			{"id":"e2","company":"Beta","title":"Lead","start":"2019-01","end":"2022-02"}
		]
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if len(me.Experience) != 2 {
		t.Fatalf("experience count: got %d, want 2", len(me.Experience))
	}
	if !me.Experience[0].Current {
		t.Errorf("experience[0].Current: got false, want true (no end date)")
	}
	if me.Experience[0].End != "" {
		t.Errorf("experience[0].End: got %q, want empty", me.Experience[0].End)
	}
	if me.Experience[1].Current {
		t.Errorf("experience[1].Current: got true, want false")
	}
	if me.Experience[1].End != "2022-02" {
		t.Errorf("experience[1].End: got %q, want 2022-02", me.Experience[1].End)
	}
}

func TestImport_ExperienceRichTextToMarkdown(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"experience": [
			{"id":"e1","company":"Acme","title":"Engineer","start":"2020",
			 "summary":"<p>Worked on <strong>distributed systems</strong>.</p><ul><li>Go microservices</li><li>Kubernetes</li></ul>"}
		]
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if len(me.Experience) == 0 {
		t.Fatal("no experience imported")
	}
	summary := me.Experience[0].Summary
	if !strings.Contains(summary, "**distributed systems**") {
		t.Errorf("summary missing bold: %q", summary)
	}
	if !strings.Contains(summary, "- Go microservices") {
		t.Errorf("summary missing list item: %q", summary)
	}
}

func TestImport_MyDataAvailability(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"availability": {
			"noticeValue": 2,
			"noticeUnit": "week",
			"startDate": "2025-03-01",
			"noticeSet": true
		}
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if me.Availability == nil {
		t.Fatal("Availability: nil, want set")
	}
	if me.Availability.NoticeValue == nil || *me.Availability.NoticeValue != 2 {
		t.Errorf("NoticeValue: got %v, want 2", me.Availability.NoticeValue)
	}
	if me.Availability.NoticeUnit != "week" {
		t.Errorf("NoticeUnit: got %q, want week", me.Availability.NoticeUnit)
	}
	if me.Availability.StartDate != "2025-03-01" {
		t.Errorf("StartDate: got %q", me.Availability.StartDate)
	}
}

func TestImport_MyDataCurrentLocation(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"geography": {
			"currentLocationSet": true,
			"currentLocation": {"country": "Germany", "city": "Berlin"}
		}
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if me.CurrentLocation == nil {
		t.Fatal("CurrentLocation: nil")
	}
	if me.CurrentLocation.Country != "Germany" {
		t.Errorf("Country: got %q", me.CurrentLocation.Country)
	}
	if me.CurrentLocation.City != "Berlin" {
		t.Errorf("City: got %q", me.CurrentLocation.City)
	}
}

func TestImport_MyDataWorkAuthorizations(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"geography": {
			"workAuthorizationsSet": true,
			"workAuthorizations": [
				{"country":"DE","status":"eligible"},
				{"country":"US","status":"need_visa"}
			]
		}
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if len(me.WorkAuthorizations) != 2 {
		t.Fatalf("WorkAuthorizations: got %d, want 2", len(me.WorkAuthorizations))
	}
}

func TestImport_MyDataDemographics(t *testing.T) {
	fromDir := makeOldDir(t, "")
	userData := `{
		"demographics": {
			"gender": "non-binary",
			"race": ["asian"],
			"ethnicity": "hispanic"
		}
	}`
	insertUserRow(t, fromDir, userData)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	me := readMeData(t, targetDir)
	if me.Demographics == nil {
		t.Fatal("Demographics: nil")
	}
	if me.Demographics.Gender != "non-binary" {
		t.Errorf("Gender: got %q", me.Demographics.Gender)
	}
	if me.Demographics.Ethnicity != "hispanic" {
		t.Errorf("Ethnicity: got %q", me.Demographics.Ethnicity)
	}
}

func TestImport_ProfileOfficeBecomesOnsite(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Solo", "2024-01-01T00:00:00Z", false)
	_, err := odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"workModes":{"modes":["remote","office","hybrid"]}}`)
	if err != nil {
		t.Fatalf("set p1 data: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	wantModes := map[string]bool{"remote": true, "hybrid": true, "onsite": true}
	for _, m := range pd.WorkModes {
		if !wantModes[m] {
			t.Errorf("unexpected work mode %q", m)
		}
		delete(wantModes, m)
	}
	if len(wantModes) > 0 {
		t.Errorf("missing work modes: %v", wantModes)
	}
	if containsStr(pd.WorkModes, "office") {
		t.Error("work modes still contain 'office', want 'onsite'")
	}
}

func TestImport_ProfileRelocationMapping(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "One", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "p2", "Two", "2024-02-01T00:00:00Z", false)
	insertProfile(t, odb, "p3", "Three", "2024-03-01T00:00:00Z", false)

	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`, `{"geography":{"willingToRelocate":true}}`)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p2'`, `{"geography":{"willingToRelocate":false}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd1 := readProfileData(t, targetDir, 1)
	pd2 := readProfileData(t, targetDir, 2)
	pd3 := readProfileData(t, targetDir, 3)

	if pd1.Relocation != "yes" {
		t.Errorf("profile 1 relocation: got %q, want yes", pd1.Relocation)
	}
	if pd2.Relocation != "no" {
		t.Errorf("profile 2 relocation: got %q, want no", pd2.Relocation)
	}
	if pd3.Relocation != "" {
		t.Errorf("profile 3 relocation: got %q, want empty (not set)", pd3.Relocation)
	}
}

func TestImport_ProfileTimeZoneBandIDs(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Zeal", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"geography":{"timezoneIds":["tz-americas","tz-europe"]}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if len(pd.TimeZones) != 2 {
		t.Errorf("TimeZones: got %d, want 2", len(pd.TimeZones))
	}
	if !containsStr(pd.TimeZones, "tz-americas") || !containsStr(pd.TimeZones, "tz-europe") {
		t.Errorf("TimeZones: got %v", pd.TimeZones)
	}
}

func TestImport_ProfileCompensation(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Pay", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"compensation":{"cashMin":80000,"cashTarget":100000,"currency":"EUR","grossNet":"gross","period":"year","equityOnTop":true,"dayRates":{"min":500,"target":650,"currency":"EUR"}}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if pd.Compensation == nil {
		t.Fatal("Compensation: nil")
	}
	if pd.Compensation.Currency != "EUR" {
		t.Errorf("Currency: got %q", pd.Compensation.Currency)
	}
	if pd.Compensation.CashMin == nil || *pd.Compensation.CashMin != 80000 {
		t.Errorf("CashMin: got %v", pd.Compensation.CashMin)
	}
	if pd.Compensation.EquityOnTop == nil || !*pd.Compensation.EquityOnTop {
		t.Errorf("EquityOnTop: want true")
	}
	if pd.Compensation.DayRates == nil {
		t.Fatal("DayRates: nil")
	}
	if pd.Compensation.DayRates.Target == nil || *pd.Compensation.DayRates.Target != 650 {
		t.Errorf("DayRates.Target: got %v", pd.Compensation.DayRates.Target)
	}
}

func TestImport_ProfileOtherRequirementsRichText(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Req", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"otherRequirements":{"text":"<p>Must allow <strong>remote work</strong>.</p>"}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if !strings.Contains(pd.OtherRequirements, "**remote work**") {
		t.Errorf("OtherRequirements: got %q, want bold remote work", pd.OtherRequirements)
	}
}

func TestImport_ProfileLegacyOnlyAppended(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Leg", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"geography":{"current":"Europe","desired":["UK","Germany"]}}`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if !strings.Contains(pd.OtherRequirements, "previous version") {
		t.Errorf("OtherRequirements: legacy text not appended: %q", pd.OtherRequirements)
	}
	if !strings.Contains(pd.OtherRequirements, "Europe") {
		t.Errorf("OtherRequirements missing 'Europe': %q", pd.OtherRequirements)
	}
	if !strings.Contains(report, "folded") {
		t.Errorf("report missing folded entry: %s", report)
	}
}

func TestImport_ProfileStructuredLocDropsLegacy(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Both", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"geography":{"preferredLocationsSet":true,"preferredLocations":[{"country":"DE","city":"Berlin"}],"current":"Old Europe"}}`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if len(pd.PreferredLocations) != 1 {
		t.Errorf("PreferredLocations: got %d, want 1", len(pd.PreferredLocations))
	}
	if strings.Contains(pd.OtherRequirements, "Old Europe") {
		t.Errorf("OtherRequirements should not contain dropped legacy: %q", pd.OtherRequirements)
	}
	if !strings.Contains(report, "dropped") {
		t.Errorf("report missing dropped entry: %s", report)
	}
}

func TestImport_MyDataLegacyGoesToFirstProfile(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "First", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "p2", "Second", "2024-02-01T00:00:00Z", false)

	userData := `{"geography":{"current":"Old location"}}`
	insertUserRow(t, fromDir, userData)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd1 := readProfileData(t, targetDir, 1)
	pd2 := readProfileData(t, targetDir, 2)

	if !strings.Contains(pd1.OtherRequirements, "previous version") {
		t.Errorf("first profile OtherRequirements missing legacy: %q", pd1.OtherRequirements)
	}
	if strings.Contains(pd2.OtherRequirements, "previous version") {
		t.Errorf("second profile should not have legacy text: %q", pd2.OtherRequirements)
	}
}

func TestImport_ProfileHeadlineImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Solo", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET headline='Senior Backend Engineer' WHERE id='p1'`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if pd.Headline != "Senior Backend Engineer" {
		t.Errorf("Headline: got %q, want 'Senior Backend Engineer'", pd.Headline)
	}
}

func TestImport_ProfileHeadlineFallsBackToUserHeadline(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "One", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "p2", "Two", "2024-02-01T00:00:00Z", false)

	userData := `{"identity":{"headline":"Platform Lead"}}`
	insertUserRow(t, fromDir, userData)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd1 := readProfileData(t, targetDir, 1)
	pd2 := readProfileData(t, targetDir, 2)

	if pd1.Headline != "Platform Lead" {
		t.Errorf("first profile Headline: got %q, want 'Platform Lead'", pd1.Headline)
	}
	if pd2.Headline != "" {
		t.Errorf("second profile Headline: got %q, want empty", pd2.Headline)
	}
}

func TestImport_ProfileEngagementAndHours(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Hours", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"engagement":{"types":["employment","contract"]},"hours":{"mode":"full_time"}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if len(pd.Engagement) != 2 {
		t.Errorf("Engagement: got %d, want 2", len(pd.Engagement))
	}
	if pd.Hours == nil || pd.Hours.Mode != "full_time" {
		t.Errorf("Hours.Mode: got %v", pd.Hours)
	}
}

func TestImport_ActiveProfileMapped(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "old-p1", "First", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "old-p2", "Second", "2024-02-01T00:00:00Z", false)

	setActiveProfileSetting(t, fromDir, "old-p2")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var activeVal string
	newDB.QueryRow(`SELECT value FROM settings WHERE key='active_profile'`).Scan(&activeVal)
	if activeVal != "2" {
		t.Errorf("active_profile: got %q, want 2", activeVal)
	}
	if !strings.Contains(report, "Active profile: set") {
		t.Errorf("report missing active profile line: %s", report)
	}
}

func TestImport_PhotoCopied(t *testing.T) {
	fromDir := makeOldDir(t, "")

	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
		0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54,
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01,
		0xE2, 0x21, 0xBC, 0x33,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	avatarDir := filepath.Join(fromDir, "profile-avatar")
	if err := os.MkdirAll(avatarDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	photoName := "profile-avatar/test-photo.png"
	if err := os.WriteFile(filepath.Join(fromDir, photoName), pngData, 0600); err != nil {
		t.Fatalf("write photo: %v", err)
	}
	meta := `{"storagePath":"` + photoName + `","contentType":"image/png","byteSize":` + itoa(len(pngData)) + `}`
	if err := os.WriteFile(filepath.Join(fromDir, "profile-avatar.json"), []byte(meta), 0600); err != nil {
		t.Fatalf("write avatar meta: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	photoPath := filepath.Join(targetDir, "files", "me_photo")
	if _, err := os.Stat(photoPath); os.IsNotExist(err) {
		t.Errorf("photo not copied to %s", photoPath)
	}
	if !strings.Contains(report, "Photo: copied") {
		t.Errorf("report missing photo line: %s", report)
	}
}

func TestImport_PhotoSkippedWrongType(t *testing.T) {
	fromDir := makeOldDir(t, "")

	avatarDir := filepath.Join(fromDir, "profile-avatar")
	os.MkdirAll(avatarDir, 0700)
	fakeName := "profile-avatar/not-an-image.jpg"
	os.WriteFile(filepath.Join(fromDir, fakeName), []byte("not an image at all"), 0600)
	meta := `{"storagePath":"` + fakeName + `","contentType":"image/jpeg","byteSize":19}`
	os.WriteFile(filepath.Join(fromDir, "profile-avatar.json"), []byte(meta), 0600)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	photoPath := filepath.Join(targetDir, "files", "me_photo")
	if _, err := os.Stat(photoPath); !os.IsNotExist(err) {
		t.Errorf("photo should not have been copied")
	}
	if !strings.Contains(report, "Photo: skipped") {
		t.Errorf("report missing photo skipped line: %s", report)
	}
}

func TestImport_QAImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	insertUserRow(t, fromDir, `{}`)
	insertQARow(t, fromDir, "qa1", "What is your notice period?", "Two weeks.", "2024-05-01T00:00:00Z")
	insertQARow(t, fromDir, "qa2", "Are you open to relocation?", "Yes, within Europe.", "2024-05-02T00:00:00Z")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var count int
	newDB.QueryRow(`SELECT count(*) FROM qa`).Scan(&count)
	if count != 2 {
		t.Errorf("qa count: got %d, want 2", count)
	}

	var q1, a1 string
	newDB.QueryRow(`SELECT question, answer FROM qa ORDER BY id LIMIT 1`).Scan(&q1, &a1)
	if q1 != "What is your notice period?" {
		t.Errorf("qa[0].question: got %q", q1)
	}
	if a1 != "Two weeks." {
		t.Errorf("qa[0].answer: got %q", a1)
	}
	if !strings.Contains(report, "Questions & Answers: 2 read, 2 written") {
		t.Errorf("report missing QA line: %s", report)
	}
}

func TestImport_EmptyCompensationInheritsFromFirst(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "One", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "p2", "Two", "2024-02-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"compensation":{"cashMin":80000,"cashTarget":100000,"currency":"EUR","grossNet":"gross","period":"year"}}`)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p2'`,
		`{"compensation":{"cashMin":null,"cashTarget":null,"equityOnTop":null,"dayRates":{"min":null,"target":null}}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd1 := readProfileData(t, targetDir, 1)
	pd2 := readProfileData(t, targetDir, 2)

	if pd1.Compensation == nil {
		t.Fatal("profile 1 Compensation: nil, want set")
	}
	if pd2.Compensation != nil {
		t.Errorf("profile 2 own Compensation: got %+v, want nil (should be absent for inheritance)", pd2.Compensation)
	}
	effective := candidate.Effective(pd2, pd1)
	if effective.Compensation == nil {
		t.Error("effective profile 2 Compensation: nil, want inherited from profile 1")
	} else if effective.Compensation.Currency != "EUR" {
		t.Errorf("effective profile 2 Compensation.Currency: got %q, want EUR", effective.Compensation.Currency)
	}
}

func TestImport_RelocationListAlwaysFolded(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Solo", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"geography":{"willingToRelocate":true,"relocation":["London","Berlin"]}}`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if pd.Relocation != "yes" {
		t.Errorf("Relocation: got %q, want yes", pd.Relocation)
	}
	if !strings.Contains(pd.OtherRequirements, "London") {
		t.Errorf("OtherRequirements should contain folded relocation list, got: %q", pd.OtherRequirements)
	}
	if !strings.Contains(report, "folded") {
		t.Errorf("report should mention folded relocation: %s", report)
	}
}

func TestImport_NoticePeriodFreeTextFolded(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Solo", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"availability":{"noticePeriod":"4 weeks notice required","startDate":""}}`)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if !strings.Contains(pd.OtherRequirements, "4 weeks notice required") {
		t.Errorf("OtherRequirements: notice period not folded: %q", pd.OtherRequirements)
	}
	if !strings.Contains(pd.OtherRequirements, "previous version") {
		t.Errorf("OtherRequirements: missing previous version label: %q", pd.OtherRequirements)
	}
}

func TestImport_NoticePeriodFreeTextDroppedWhenStructured(t *testing.T) {
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "Solo", "2024-01-01T00:00:00Z", false)
	odb.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"availability":{"noticePeriod":"old notice text","startDate":""}}`)
	insertUserRow(t, fromDir, `{"availability":{"noticeSet":true,"noticeValue":4,"noticeUnit":"week","startDate":""}}`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	pd := readProfileData(t, targetDir, 1)
	if strings.Contains(pd.OtherRequirements, "old notice text") {
		t.Errorf("OtherRequirements: dropped notice text should not appear: %q", pd.OtherRequirements)
	}
	if !strings.Contains(report, "dropped") {
		t.Errorf("report should mention dropped notice period: %s", report)
	}
}

func TestImport_UserTargetRolesReportedAsNotImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	insertUserRow(t, fromDir, `{
		"targetRoles": ["Software Engineer","Staff Engineer"]
	}`)
	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, "targetRoles") {
		t.Errorf("report should mention targetRoles as not imported: %s", report)
	}
	if !strings.Contains(report, "not imported") {
		t.Errorf("report should say not imported: %s", report)
	}
}

func TestImport_EmptyUserTargetRolesNotReported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	insertUserRow(t, fromDir, `{"targetRoles":[],"targetIndustries":[]}`)
	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if strings.Contains(report, "targetRoles") {
		t.Errorf("report should not mention empty targetRoles: %s", report)
	}
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	return strings.TrimLeft(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, fmt.Sprintf("%d", n)), "")
}
