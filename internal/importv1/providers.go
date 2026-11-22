package importv1

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"gopkg.in/yaml.v3"
)

type oldProviderEntry struct {
	Type         string `yaml:"type"`
	Model        string `yaml:"model"`
	Endpoint     string `yaml:"endpoint"`
	TimeoutMS    int    `yaml:"timeout_ms"`
	ContextChars int    `yaml:"context_chars"`
}

type oldProvidersYAML struct {
	Stages struct {
		Assessment  string `yaml:"assessment"`
		Extraction  string `yaml:"extraction"`
		Preparation string `yaml:"preparation"`
	} `yaml:"stages"`
	Providers map[string]oldProviderEntry `yaml:"providers"`
}

type oldRuntimeModelFields struct {
	QuickMatch struct {
		ModelPasses struct {
			AllowPaidBulkProvider bool `yaml:"allow_paid_bulk_provider"`
		} `yaml:"model_passes"`
	} `yaml:"quick_match"`
	Paused     bool `yaml:"paused"`
	DailyLimit int  `yaml:"daily_limit"`
}

var oldProviderTypeMap = map[string]string{
	"ollama":     "ollama",
	"compatible": "openai",
	"openai":     "openai",
	"opencode":   "cli-opencode",
	"claude":     "cli-claude",
}

func importProviders(ctx context.Context, fromDir string, database *db.DB, store *config.Store, dataDir string, report *importReport) error {
	var prov oldProvidersYAML
	if b, err := os.ReadFile(filepath.Join(fromDir, "config", "providers.yaml")); err == nil {
		if err := yaml.Unmarshal(b, &prov); err != nil {
			return fmt.Errorf("reading old providers.yaml: %w", err)
		}
	}

	var rt oldRuntimeModelFields
	if b, err := os.ReadFile(filepath.Join(fromDir, "config", "runtime.yaml")); err == nil {
		if err := yaml.Unmarshal(b, &rt); err != nil {
			return fmt.Errorf("reading old runtime.yaml model fields: %w", err)
		}
	}

	srcSecrets := filepath.Join(fromDir, "secrets.env")
	if data, err := os.ReadFile(srcSecrets); err == nil {
		dstSecrets := filepath.Join(dataDir, "secrets.env")
		if err := os.WriteFile(dstSecrets, data, 0600); err != nil {
			return fmt.Errorf("writing secrets.env: %w", err)
		}
		report.SettingsCarried = append(report.SettingsCarried, "secrets.env: copied")
	}

	if len(prov.Providers) == 0 {
		return nil
	}

	providers := make(map[string]config.ProviderConfig, len(prov.Providers))
	for name, p := range prov.Providers {
		newType, ok := oldProviderTypeMap[p.Type]
		if !ok {
			continue
		}
		cost := "local"
		if p.Endpoint != "" {
			if u, err := url.Parse(p.Endpoint); err == nil {
				if !isLoopbackHost(u.Hostname()) {
					cost = "paid"
				}
			}
		}
		pc := config.ProviderConfig{
			Type:  newType,
			Model: p.Model,
			Cost:  cost,
		}
		if p.Endpoint != "" {
			pc.Endpoint = p.Endpoint
		}
		if p.TimeoutMS > 0 {
			pc.Timeout = config.Duration(time.Duration(p.TimeoutMS) * time.Millisecond)
		}
		if p.ContextChars > 0 {
			pc.Context = p.ContextChars
		}
		providers[name] = pc
		report.SettingsCarried = append(report.SettingsCarried, fmt.Sprintf("providers.%s: type=%s cost=%s", name, newType, cost))
	}

	reading := prov.Stages.Assessment
	if reading == "" {
		reading = prov.Stages.Extraction
	}
	stages := map[string]string{"reading": reading, "preparation": prov.Stages.Preparation}
	work := config.WorkConfig{}
	for _, kind := range config.WorkKinds {
		if _, ok := providers[stages[kind]]; ok && stages[kind] != "" {
			work[kind] = stages[kind]
			report.SettingsCarried = append(report.SettingsCarried, fmt.Sprintf("work.%s: %s", kind, stages[kind]))
		}
	}

	if err := store.Update(func(c *config.Config) error {
		c.Providers = providers
		if len(work) > 0 {
			c.Work = work
		}
		if rt.QuickMatch.ModelPasses.AllowPaidBulkProvider {
			c.AllowPaidBulk = true
			report.SettingsCarried = append(report.SettingsCarried, "allow_paid_bulk: true")
		}
		if rt.DailyLimit > 0 {
			c.DailyModelLimit = rt.DailyLimit
			report.SettingsCarried = append(report.SettingsCarried, fmt.Sprintf("daily_model_limit: %d", rt.DailyLimit))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("writing provider settings: %w", err)
	}

	sort.Strings(report.SettingsCarried)

	if rt.Paused {
		if _, err := database.ExecContext(ctx, `INSERT OR REPLACE INTO settings (key,value) VALUES ('models_paused','true')`); err != nil {
			return fmt.Errorf("setting models paused: %w", err)
		}
	}

	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	return strings.HasPrefix(host, "127.")
}
