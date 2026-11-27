package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/config"
)

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sifty-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func loadFile(t *testing.T, path string) (config.Config, error) {
	t.Helper()
	s, result, err := config.Open(path)
	if err != nil {
		return config.Config{}, err
	}
	if result.Err != "" {
		return config.Config{}, fmt.Errorf("recovered: %s", result.Err)
	}
	return s.Get(), nil
}

func loadContent(t *testing.T, content string) (config.Config, error) {
	t.Helper()
	return loadFile(t, writeTmp(t, content))
}

func TestDefaultsAreValid(t *testing.T) {
	c := config.Defaults()
	if err := config.Validate(c); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestDefaultsIncludeCollectionKeys(t *testing.T) {
	d := config.Defaults()
	if !d.Collection.Auto {
		t.Error("collection.auto default: want true")
	}
	if time.Duration(d.Collection.Interval) != 3*time.Hour {
		t.Errorf("collection.interval default: got %s, want 3h", time.Duration(d.Collection.Interval))
	}
	if len(d.Sources) != 0 {
		t.Errorf("sources default: want empty, got %d items", len(d.Sources))
	}
}

func TestDefaultsYAMLAccepted(t *testing.T) {
	yaml := config.DefaultsYAML()
	c, err := loadContent(t, yaml)
	if err != nil {
		t.Fatalf("config defaults YAML is rejected: %v", err)
	}
	if c.Port != config.Defaults().Port {
		t.Errorf("port: got %d, want %d", c.Port, config.Defaults().Port)
	}
	if !c.Collection.Auto {
		t.Error("collection.auto: want true after round-trip")
	}
	if time.Duration(c.Collection.Interval) != 3*time.Hour {
		t.Errorf("collection.interval: got %s, want 3h", time.Duration(c.Collection.Interval))
	}
}

func TestMissingFileReturnsDefaults(t *testing.T) {
	c, err := loadFile(t, filepath.Join(t.TempDir(), "nosuchfile.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	d := config.Defaults()
	if c.Port != d.Port || c.Debug != d.Debug ||
		c.Collection.Auto != d.Collection.Auto ||
		c.Collection.Interval != d.Collection.Interval ||
		len(c.Sources) != 0 {
		t.Errorf("missing file: got %+v, want defaults %+v", c, d)
	}
}

func TestValidKeysParsed(t *testing.T) {
	parse := func(yaml string) config.Config {
		t.Helper()
		c, err := loadContent(t, yaml)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return c
	}
	if c := parse("port: 9090\n"); c.Port != 9090 {
		t.Errorf("port: got %d, want 9090", c.Port)
	}
	if c := parse("debug: true\n"); !c.Debug {
		t.Error("debug should be true")
	}
	if c := parse("collection:\n  auto: false\n"); c.Collection.Auto {
		t.Error("collection.auto: want false")
	}
	if c := parse("collection:\n  interval: 30m\n"); time.Duration(c.Collection.Interval) != 30*time.Minute {
		t.Errorf("collection.interval: got %s, want 30m", time.Duration(c.Collection.Interval))
	}
}

func TestUnknownKeysAreIgnored(t *testing.T) {
	for _, yaml := range []string{
		"unknown_key: true\n",
		"collection:\n  unknown: true\n",
		"sources:\n  - url: https://example.com/jobs\n    mystery: true\n",
	} {
		c, err := loadContent(t, yaml)
		if err != nil {
			t.Errorf("unknown key in %q should be ignored, got error: %v", yaml, err)
			continue
		}
		if c.Port != config.Defaults().Port {
			t.Errorf("unknown key changed defaults: port = %d", c.Port)
		}
	}
}

func TestSourceWithValidURL(t *testing.T) {
	yaml := "sources:\n  - url: https://example.com/jobs\n    enabled: true\n"
	c, err := loadContent(t, yaml)
	if err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	if len(c.Sources) != 1 {
		t.Fatalf("want 1 source, got %d", len(c.Sources))
	}
	if c.Sources[0].URL != "https://example.com/jobs" {
		t.Errorf("url: got %q", c.Sources[0].URL)
	}
	if !c.Sources[0].Enabled {
		t.Error("enabled: want true")
	}
}

func TestSourceEnabledDefaultsToTrue(t *testing.T) {
	yaml := "sources:\n  - url: https://example.com/jobs\n"
	c, err := loadContent(t, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.Sources[0].Enabled {
		t.Error("enabled should default to true when absent")
	}
}

func TestSourceInvalidURL(t *testing.T) {
	for _, bad := range []string{
		"not-a-url",
		"ftp://example.com/jobs",
		"/relative/path",
		"http://",
		"https://user:pass@example.com/jobs",
	} {
		yaml := "sources:\n  - url: " + bad + "\n"
		_, err := loadContent(t, yaml)
		if err == nil {
			t.Errorf("invalid url %q should be rejected", bad)
			continue
		}
		if !strings.Contains(err.Error(), "Enter an http(s) address.") {
			t.Errorf("error should say %q for %q: %v", "Enter an http(s) address.", bad, err)
		}
	}
}

func TestSourceNameLimitCountsCharactersNotBytes(t *testing.T) {
	name100 := strings.Repeat("ñ", 100)
	yaml100 := "sources:\n  - name: " + name100 + "\n    url: https://example.com/jobs\n"
	if _, err := loadContent(t, yaml100); err != nil {
		t.Errorf("100-character non-ASCII name should be accepted: %v", err)
	}
	name101 := strings.Repeat("ñ", 101)
	yaml101 := "sources:\n  - name: " + name101 + "\n    url: https://example.com/jobs\n"
	_, err := loadContent(t, yaml101)
	if err == nil {
		t.Error("101-character non-ASCII name should be rejected")
	} else if !strings.Contains(err.Error(), "The name is at most 100 characters.") {
		t.Errorf("error should say %q: %v", "The name is at most 100 characters.", err)
	}
}

func TestDuplicateSourceKey(t *testing.T) {
	yaml := `sources:
  - url: https://www.Example.com/jobs/
  - url: https://example.com/jobs
`
	_, err := loadContent(t, yaml)
	if err == nil {
		t.Fatal("duplicate source key should be rejected")
	}
	if !strings.Contains(err.Error(), "This source is already in the list.") {
		t.Errorf("error should say %q: %v", "This source is already in the list.", err)
	}
}

func TestSourceKeyTransformations(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://EXAMPLE.COM/jobs", "https://example.com/jobs"},
		{"https://www.example.com/jobs", "https://example.com/jobs"},
		{"https://example.com/jobs/", "https://example.com/jobs"},
		{"https://example.com/jobs?type=go", "https://example.com/jobs?type=go"},
		{"https://example.com/jobs#section", "https://example.com/jobs"},
		{"https://www.Example.com/jobs/", "https://example.com/jobs"},
	}
	for _, tc := range cases {
		k, err := config.SourceKey(tc.in)
		if err != nil {
			t.Errorf("SourceKey(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if k != tc.want {
			t.Errorf("SourceKey(%q) = %q, want %q", tc.in, k, tc.want)
		}
	}
}

func TestSourceKeyErrors(t *testing.T) {
	bad := []string{"ftp://example.com/jobs", "example.com/jobs", "/path",
		"https://user:pass@example.com/jobs"}
	for _, url := range bad {
		if _, err := config.SourceKey(url); err == nil {
			t.Errorf("SourceKey(%q) should return error", url)
		}
	}
}

func TestFullConfigRoundTrip(t *testing.T) {
	yaml := `port: 8080
collection:
  auto: false
  interval: 1h
sources:
  - name: Go Jobs
    url: https://example.com/go-jobs
    enabled: true
  - url: http://jobs.example.org/
    enabled: false
`
	c, err := loadContent(t, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Port != 8080 {
		t.Errorf("port: got %d", c.Port)
	}
	if c.Collection.Auto {
		t.Error("collection.auto: want false")
	}
	if time.Duration(c.Collection.Interval) != time.Hour {
		t.Errorf("collection.interval: got %s", time.Duration(c.Collection.Interval))
	}
	if len(c.Sources) != 2 {
		t.Fatalf("sources: want 2, got %d", len(c.Sources))
	}
	if c.Sources[0].Name != "Go Jobs" {
		t.Errorf("sources[0].name: got %q", c.Sources[0].Name)
	}
	if c.Sources[1].URL != "http://jobs.example.org/" {
		t.Errorf("sources[1].url: got %q", c.Sources[1].URL)
	}
	if c.Sources[1].Enabled {
		t.Error("sources[1].enabled: want false")
	}
}

func TestProviderAPIKeyInSettingsRefused(t *testing.T) {
	_, err := loadContent(t, `
providers:
  local:
    type: ollama
    api_key: mysecret
`)
	if err == nil {
		t.Fatal("expected error for api_key in settings, got nil")
	}
	if !strings.Contains(err.Error(), "A key is never kept in the settings file.") || strings.Contains(err.Error(), "mysecret") {
		t.Errorf("error message: %v", err)
	}
}

func TestWeightsAndRatesKeepDefaultsForAbsentKeys(t *testing.T) {
	c, err := loadContent(t, "weights:\n  role: 0.5\nrates:\n  SEK: 0.09\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Weights["role"] != 0.5 || c.Weights["money"] != 0.20 || c.Rates["SEK"] != 0.09 || c.Rates["USD"] != 0.92 {
		t.Errorf("weights %v rates %v", c.Weights, c.Rates)
	}
	for _, bad := range []string{"weights:\n  luck: 0.1\n", "weights:\n  role: -1\n", "rates:\n  USD: 0\n"} {
		if _, err := loadContent(t, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const ageLimitSentence = "The age limit is a whole number of months, from 1 to 1200, or empty for no limit."

func TestAgeLimitInTheFileFollowsTheSameBoundAsTheEndpoint(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"1200", 1200}, {"", 0}} {
		c, err := loadContent(t, "collection:\n  max_age_months: "+tc.value+"\n")
		if err != nil || c.Collection.MaxAgeMonths != tc.want {
			t.Errorf("max_age_months %q: got %d, %v; want %d", tc.value, c.Collection.MaxAgeMonths, err, tc.want)
		}
	}
	for _, bad := range []string{"-3", "0", "1201", "99999999999"} {
		if _, err := loadContent(t, "collection:\n  max_age_months: "+bad+"\n"); err == nil || !strings.Contains(err.Error(), ageLimitSentence) {
			t.Errorf("max_age_months %s: got %v; want %q", bad, err, ageLimitSentence)
		}
	}
}

func TestRefusedSettingsAreSentencesWhetherSavedOrStandingInTheFile(t *testing.T) {
	const providers = "providers:\n  localbox: {type: ollama}\n"
	localbox := map[string]config.ProviderConfig{"localbox": {Type: "ollama"}}
	cases := []struct {
		name     string
		yaml     string
		change   func(c *config.Config)
		sentence string
	}{
		{"unknown provider type", "providers:\n  localbox: {type: carrier-pigeon}\n",
			func(c *config.Config) {
				c.Providers = map[string]config.ProviderConfig{"localbox": {Type: "carrier-pigeon"}}
			},
			"The type of the provider called localbox is not known. A provider is one of ollama, openai, cli-claude or cli-opencode."},
		{"unknown provider cost", "providers:\n  localbox: {type: ollama, cost: cheap}\n",
			func(c *config.Config) {
				c.Providers = map[string]config.ProviderConfig{"localbox": {Type: "ollama", Cost: "cheap"}}
			},
			"The cost of the provider called localbox must be local, free or paid."},
		{"unknown provider for a kind of work", providers + "work: {reading: nowhere}\n",
			func(c *config.Config) { c.Providers, c.Work = localbox, config.WorkConfig{"reading": "nowhere"} },
			"There is no provider called nowhere to use for Reading."},
		{"negative weight", "weights: {role: -1}\n",
			func(c *config.Config) { c.Weights["role"] = -1 },
			"The weight for role must be a number of 0 or more."},
		{"weight of an unknown criterion", "weights: {luck: 0.1}\n",
			func(c *config.Config) { c.Weights["luck"] = 0.1 },
			"luck is not a criterion of Fit, so it has no weight."},
		{"rate of zero", "rates: {USD: 0}\n",
			func(c *config.Config) { c.Rates["USD"] = 0 },
			"The rate for USD must be a number above 0."},
		{"rate with a bad code", "rates: {usd: 1}\n",
			func(c *config.Config) { c.Rates["usd"] = 1 },
			"usd is not a currency code: a code is three capital letters, such as USD."},
		{"port out of range", "port: 99999\n",
			func(c *config.Config) { c.Port = 99999 },
			"The port must be between 0 and 65535."},
		{"interval too short", "collection: {interval: 10m}\n",
			func(c *config.Config) { c.Collection.Interval = config.Duration(10 * time.Minute) },
			config.IntervalRefusal},
		{"interval too long", "collection: {interval: 200h}\n",
			func(c *config.Config) { c.Collection.Interval = config.Duration(200 * time.Hour) },
			config.IntervalRefusal},
		{"interval that is not a time", "collection: {interval: soon}\n", nil, config.IntervalRefusal},
		{"timeout that is not a time", "providers:\n  localbox: {type: ollama, timeout: later}\n", nil,
			"The timeout of the provider called localbox is a number with a unit, such as 30s or 2m."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, result, err := config.Open(writeTmp(t, tc.yaml))
			if err != nil || result.Err != tc.sentence {
				t.Errorf("file: got %q, %v; want %q", result.Err, err, tc.sentence)
			}
			if tc.change == nil {
				return
			}
			s, _ := openStore(t)
			err = s.Update(func(c *config.Config) error { tc.change(c); return nil })
			var inv apperr.Invalid
			if !errors.As(err, &inv) || inv.Message != tc.sentence {
				t.Errorf("save: got %v; want %q", err, tc.sentence)
			}
		})
	}
}
