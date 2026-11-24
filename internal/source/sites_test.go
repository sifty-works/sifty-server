package source_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/source"
)

func writeSitesYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sites.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resetSites(t *testing.T) {
	t.Helper()
	path := writeSitesYAML(t, "sites: []\n")
	source.LoadSites(path)
}

func TestEmbeddedSitesGiveCorrectModule(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
	}{
		{"web3.career", "links"},
		{"web3.careers", "links"},
		{"remote3.co", "feed"},
		{"weworkremotely.com", "feed"},
		{"cryptocurrencyjobs.co", "feed"},
		{"jobs.framework.ventures", "getro"},
		{"a16zfrontier.com", "a16z"},
		{"paradigm.xyz", "paradigm"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			u, _ := url.Parse("https://" + tc.host + "/")
			mod, _ := source.For(u)
			if mod.Name() != tc.want {
				t.Errorf("For(%q).Name() = %q, want %q", tc.host, mod.Name(), tc.want)
			}
		})
	}
}

func TestLoadSitesValidOverrideReplacesEntry(t *testing.T) {
	t.Cleanup(func() { resetSites(t) })
	path := writeSitesYAML(t, "sites:\n  - host: web3.career\n    module: feed\n    list_address: https://web3.career/rss\n")
	broken, err := source.LoadSites(path)
	if err != nil || broken != "" {
		t.Fatalf("valid override should produce no recovery, got broken=%q err=%v", broken, err)
	}
	u, _ := url.Parse("https://web3.career/")
	mod, site := source.For(u)
	if mod.Name() != "feed" {
		t.Errorf("module = %q, want feed after override", mod.Name())
	}
	if site.ListAddress != "https://web3.career/rss" {
		t.Errorf("list_address = %q", site.ListAddress)
	}
}

func TestLoadSitesOverrideAddsNewHostAndKeepsEmbedded(t *testing.T) {
	t.Cleanup(func() { resetSites(t) })
	path := writeSitesYAML(t, "sites:\n  - host: newboard.example.com\n    module: feed\n    list_address: https://newboard.example.com/rss\n")
	broken, err := source.LoadSites(path)
	if err != nil || broken != "" {
		t.Fatalf("valid override should produce no recovery, got broken=%q err=%v", broken, err)
	}
	newU, _ := url.Parse("https://newboard.example.com/")
	if mod, _ := source.For(newU); mod.Name() != "feed" {
		t.Errorf("new host module = %q, want feed", mod.Name())
	}
	embU, _ := url.Parse("https://web3.career/")
	if mod, _ := source.For(embU); mod.Name() != "links" {
		t.Errorf("embedded host = %q after override, want links unchanged", mod.Name())
	}
}

func TestLoadSitesUnknownModuleRenamesFileAndKeepsEmbedded(t *testing.T) {
	path := writeSitesYAML(t, "sites:\n  - host: board.example.com\n    module: badmodule\n")
	broken, err := source.LoadSites(path)
	if err == nil {
		t.Fatal("unknown module should produce a recovery")
	}
	if broken == "" {
		t.Error("file with unknown module should be renamed")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("original path should not exist after rename")
	}
	u, _ := url.Parse("https://web3.career/")
	if mod, _ := source.For(u); mod.Name() != "links" {
		t.Errorf("embedded module = %q after rejected override, want links", mod.Name())
	}
}

func TestLoadSitesBadLinkPatternRenamesFile(t *testing.T) {
	path := writeSitesYAML(t, "sites:\n  - host: board.example.com\n    module: links\n    link_pattern: )(\n")
	broken, err := source.LoadSites(path)
	if err == nil {
		t.Fatal("bad link_pattern should produce a recovery")
	}
	if broken == "" {
		t.Error("file with bad link_pattern should be renamed")
	}
	var refused source.SitesRefused
	if !errors.As(err, &refused) || refused.Reason != "The host board.example.com has a link pattern that is not a valid regular expression." {
		t.Errorf("reason = %q", refused.Reason)
	}
}

func TestLoadSitesDuplicateHostRenamesFile(t *testing.T) {
	path := writeSitesYAML(t, "sites:\n  - host: board.example.com\n    module: feed\n  - host: board.example.com\n    module: links\n")
	broken, err := source.LoadSites(path)
	if err == nil {
		t.Fatal("duplicate host should produce a recovery")
	}
	if broken == "" {
		t.Error("file with duplicate host should be renamed")
	}
	var refused source.SitesRefused
	if !errors.As(err, &refused) || refused.Reason != "The host board.example.com is listed twice." {
		t.Errorf("reason = %q", refused.Reason)
	}
}

func listNamedFeed(t *testing.T, contentType, body string) ([]string, error) {
	t.Helper()
	t.Cleanup(func() { resetSites(t) })
	if _, err := source.LoadSites(writeSitesYAML(t, "sites:\n  - host: feedboard.example.com\n    module: feed\n")); err != nil {
		t.Fatal(err)
	}
	addr := "https://feedboard.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, ContentType: contentType, Body: []byte(body)}})
	u, _ := url.Parse(addr)
	mod, site := source.For(u)
	if mod.Name() != "feed" {
		t.Fatalf("module = %q, want feed", mod.Name())
	}
	ps, err := mod.List(context.Background(), m, site)
	var urls []string
	for _, p := range ps {
		urls = append(urls, p.URL)
	}
	return urls, err
}

func TestNamedFeedSourceWithNoItemsRecognisesNothing(t *testing.T) {
	_, err := listNamedFeed(t, "application/rss+xml", `<rss version="2.0"><channel></channel></rss>`)
	if err != source.ErrNothingRecognised {
		t.Errorf("error = %v, want ErrNothingRecognised", err)
	}
}

func TestNamedFeedSourceReadsFeedWhateverTheContentType(t *testing.T) {
	urls, err := listNamedFeed(t, "text/html", `<rss version="2.0"><channel><item><title>Engineer at Acme</title><link>https://feedboard.example.com/jobs/1</link></item></channel></rss>`)
	if err != nil || len(urls) != 1 {
		t.Fatalf("List err=%v urls=%v", err, urls)
	}
}

func TestLoadSitesRefusesMissingEmptyAndJSONLDModule(t *testing.T) {
	for name, yaml := range map[string]string{
		"missing key": "sites:\n  - host: board.example.com\n",
		"empty name":  "sites:\n  - host: board.example.com\n    module: \"\"\n",
		"json-ld":     "sites:\n  - host: board.example.com\n    module: json-ld\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { resetSites(t) })
			if _, err := source.LoadSites(writeSitesYAML(t, yaml)); err == nil {
				t.Error("module should be refused")
			}
		})
	}
}
