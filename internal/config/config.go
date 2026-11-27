package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d Duration) String() string { return shortDuration(time.Duration(d)) }

func (d Duration) MarshalYAML() (interface{}, error) {
	return shortDuration(time.Duration(d)), nil
}

func shortDuration(d time.Duration) string {
	h := d / time.Hour
	rem := d % time.Hour
	m := rem / time.Minute
	if rem%time.Minute != 0 {
		return d.String()
	}
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return errors.New("A time is a number with a unit, such as 30s, 15m or 3h.")
	}
	*d = Duration(parsed)
	return nil
}

type CollectionConfig struct {
	Auto         bool     `yaml:"auto"`
	Interval     Duration `yaml:"interval"`
	MaxAgeMonths int      `yaml:"max_age_months,omitempty"`
}

func (c *CollectionConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawCollection CollectionConfig
	r := &rawCollection{Auto: true, Interval: Duration(3 * time.Hour)}
	if err := value.Decode(r); err != nil {
		return err
	}
	*c = CollectionConfig(*r)
	return nil
}

type SourceConfig struct {
	Name    string `yaml:"name,omitempty"`
	URL     string `yaml:"url"`
	Enabled bool   `yaml:"enabled"`
}

func (s *SourceConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawSource SourceConfig
	r := &rawSource{Enabled: true}
	if err := value.Decode(r); err != nil {
		return err
	}
	*s = SourceConfig(*r)
	return nil
}

type ProviderConfig struct {
	Type     string   `yaml:"type"`
	Model    string   `yaml:"model,omitempty"`
	Endpoint string   `yaml:"endpoint,omitempty"`
	Cost     string   `yaml:"cost,omitempty"`
	Timeout  Duration `yaml:"timeout,omitempty"`
	Context  int      `yaml:"context,omitempty"`
	KeyEnv   string   `yaml:"key_env,omitempty"`
}

type WorkConfig map[string]string

func (w WorkConfig) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, kind := range WorkKinds {
		if w[kind] != "" {
			setScalar(n, kind, w[kind], "!!str")
		}
	}
	return n, nil
}

type Config struct {
	Port            int                       `yaml:"port"`
	Debug           bool                      `yaml:"debug"`
	Collection      CollectionConfig          `yaml:"collection"`
	Sources         []SourceConfig            `yaml:"sources"`
	Weights         map[string]float64        `yaml:"weights"`
	Rates           map[string]float64        `yaml:"rates"`
	Providers       map[string]ProviderConfig `yaml:"providers,omitempty"`
	Work            WorkConfig                `yaml:"work,omitempty"`
	AllowPaidBulk   bool                      `yaml:"allow_paid_bulk,omitempty"`
	DailyModelLimit int                       `yaml:"daily_model_limit,omitempty"`
	PreciseReading  bool                      `yaml:"precise_reading,omitempty"`
}

func Defaults() Config {
	return Config{
		Port:  7373,
		Debug: false,
		Collection: CollectionConfig{
			Auto:     true,
			Interval: Duration(3 * time.Hour),
		},
		Sources: []SourceConfig{},
		Weights: map[string]float64{"role": 0.25, "money": 0.20, "location": 0.075, "remote_restriction": 0.075,
			"time_zone": 0.10, "work_mode": 0.04, "engagement": 0.03, "hours": 0.03, "requirements": 0.15},
		Rates: defaultRates(),
	}
}

type Currency struct {
	Code, Symbol string
	Rate         float64
}

var Currencies = []Currency{
	{"USD", "$", 0.92}, {"EUR", "€", 1}, {"GBP", "£", 1.17}, {"CAD", "", 0.68}, {"AUD", "", 0.60}, {"CHF", "", 1.08}, {"JPY", "", 0.0062},
}

func defaultRates() map[string]float64 {
	rates := map[string]float64{}
	for _, c := range Currencies {
		rates[c.Code] = c.Rate
	}
	return rates
}

var WorkKinds = []string{"reading", "shortlist_check", "preparation"}

var WorkLabels = map[string]string{"reading": "Reading", "shortlist_check": "Shortlist check", "preparation": "Preparation"}

var WorkNouns = map[string]string{"reading": "reading", "shortlist_check": "the Shortlist check", "preparation": "preparation"}

func (c *Config) WorkProviderFor(kind string) string {
	return c.Work[kind]
}

func (c *Config) SetWorkProvider(kind, provider string) bool {
	if !slices.Contains(WorkKinds, kind) {
		return false
	}
	if provider == "" {
		delete(c.Work, kind)
		return true
	}
	if c.Work == nil {
		c.Work = WorkConfig{}
	}
	c.Work[kind] = provider
	return true
}

func SourceKey(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("url is not an absolute http or https address")
	}
	if u.User != nil {
		return "", fmt.Errorf("url must not contain credentials")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	key := u.Scheme + "://" + host + strings.TrimRight(u.Path, "/")
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key, nil
}

const NotSettingsFileSentence = "It is not a valid settings file."

type notSettingsFile struct{ cause error }

func (e notSettingsFile) Error() string { return "settings file is not valid: " + e.cause.Error() }

func (e notSettingsFile) Unwrap() error { return e.cause }

func parse(data []byte) (Config, []string, error) {
	c := Defaults()

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return c, nil, notSettingsFile{fmt.Errorf("parsing settings file: %w", err)}
	}
	if node.Kind == 0 {
		return c, nil, nil
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return c, nil, notSettingsFile{errors.New("settings file must be a YAML mapping")}
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return c, nil, notSettingsFile{errors.New("settings file must be a YAML mapping")}
	}

	knownTop := map[string]bool{"port": true, "debug": true, "collection": true, "sources": true, "weights": true, "rates": true,
		"providers": true, "work": true, "allow_paid_bulk": true, "daily_model_limit": true, "precise_reading": true}
	knownCollection := map[string]bool{"auto": true, "interval": true, "max_age_months": true}
	knownSource := map[string]bool{"name": true, "url": true, "enabled": true}
	knownProvider := map[string]bool{"type": true, "model": true, "endpoint": true, "cost": true, "timeout": true, "context": true, "key_env": true}
	var unknown []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i].Value, root.Content[i+1]
		if !knownTop[k] {
			unknown = append(unknown, k)
			continue
		}
		switch k {
		case "collection":
			if v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					ck := v.Content[j].Value
					if !knownCollection[ck] {
						unknown = append(unknown, "collection."+ck)
					}
					if ck == "interval" && !validDurationNode(v.Content[j+1]) {
						return c, nil, errors.New(IntervalRefusal)
					}
					if ck == "max_age_months" {
						if err := checkAgeLimitNode(v.Content[j+1]); err != nil {
							return c, nil, err
						}
					}
				}
			}
		case "work":
			if v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					if wk := v.Content[j].Value; !slices.Contains(WorkKinds, wk) {
						unknown = append(unknown, "work."+wk)
					}
				}
			}
		case "sources":
			if v.Kind == yaml.SequenceNode {
				for idx, src := range v.Content {
					if src.Kind != yaml.MappingNode {
						continue
					}
					for j := 0; j+1 < len(src.Content); j += 2 {
						if sk := src.Content[j].Value; !knownSource[sk] {
							unknown = append(unknown, fmt.Sprintf("sources[%d].%s", idx, sk))
						}
					}
				}
			}
		case "providers":
			if v.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				name, pn := v.Content[j].Value, v.Content[j+1]
				if pn.Kind != yaml.MappingNode {
					continue
				}
				for n := 0; n+1 < len(pn.Content); n += 2 {
					switch pk := pn.Content[n].Value; {
					case pk == "api_key":
						return c, nil, fmt.Errorf("A key is never kept in the settings file. The provider called %s must take its key from an environment variable, or the key goes in the secrets file.", name)
					case pk == "timeout" && !validDurationNode(pn.Content[n+1]):
						return c, nil, fmt.Errorf("The timeout of the provider called %s is a number with a unit, such as 30s or 2m.", name)
					case !knownProvider[pk]:
						unknown = append(unknown, "providers."+name+"."+pk)
					}
				}
			}
		}
	}

	if err := node.Decode(&c); err != nil {
		return c, nil, notSettingsFile{fmt.Errorf("parsing settings file: %w", err)}
	}

	if err := Validate(c); err != nil {
		return c, nil, err
	}
	return c, unknown, nil
}

const MaxAgeLimitMonths = 1200

func AgeLimit(months *float64) (int, error) {
	if months == nil {
		return 0, nil
	}
	if *months < 1 || *months != math.Trunc(*months) || *months > MaxAgeLimitMonths {
		return 0, fmt.Errorf("The age limit is a whole number of months, from 1 to %d, or empty for no limit.", MaxAgeLimitMonths)
	}
	return int(*months), nil
}

func validDurationNode(n *yaml.Node) bool {
	if n.Kind != yaml.ScalarNode {
		return false
	}
	_, err := time.ParseDuration(n.Value)
	return err == nil
}

func checkAgeLimitNode(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	var months float64
	if n.Kind == yaml.ScalarNode && (n.Tag == "!!int" || n.Tag == "!!float") {
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			months = f
		}
	}
	_, err := AgeLimit(&months)
	return err
}

var ProviderTypes = []string{"ollama", "openai", "cli-claude", "cli-opencode"}

func JoinOr(items []string) string {
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

const IntervalRefusal = "The interval must be between 15 minutes and 7 days."

func Validate(c Config) error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("The port must be between 0 and 65535.")
	}
	minInterval := Duration(15 * time.Minute)
	maxInterval := Duration(168 * time.Hour)
	if c.Collection.Interval < minInterval || c.Collection.Interval > maxInterval {
		return errors.New(IntervalRefusal)
	}
	if c.Collection.MaxAgeMonths != 0 {
		months := float64(c.Collection.MaxAgeMonths)
		if _, err := AgeLimit(&months); err != nil {
			return err
		}
	}
	defaults := Defaults().Weights
	for _, k := range slices.Sorted(maps.Keys(c.Weights)) {
		if _, ok := defaults[k]; !ok {
			return fmt.Errorf("%s is not a criterion of Fit, so it has no weight.", k)
		}
		if w := c.Weights[k]; w < 0 || math.IsInf(w, 0) || math.IsNaN(w) {
			return fmt.Errorf("The weight for %s must be a number of 0 or more.", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(c.Rates)) {
		if len(k) != 3 || strings.ToUpper(k) != k {
			return fmt.Errorf("%s is not a currency code: a code is three capital letters, such as USD.", k)
		}
		if r := c.Rates[k]; !(r > 0) || math.IsInf(r, 0) {
			return fmt.Errorf("The rate for %s must be a number above 0.", k)
		}
	}
	validCosts := map[string]bool{"local": true, "free": true, "paid": true, "": true}
	for _, name := range slices.Sorted(maps.Keys(c.Providers)) {
		pc := c.Providers[name]
		if !slices.Contains(ProviderTypes, pc.Type) {
			return fmt.Errorf("The type of the provider called %s is not known. A provider is one of %s.", name, JoinOr(ProviderTypes))
		}
		if !validCosts[pc.Cost] {
			return fmt.Errorf("The cost of the provider called %s must be local, free or paid.", name)
		}
	}
	{
		for _, kind := range WorkKinds {
			pname := c.Work[kind]
			if pname == "" {
				continue
			}
			if _, ok := c.Providers[pname]; !ok {
				return fmt.Errorf("There is no provider called %s to use for %s.", pname, WorkLabels[kind])
			}
		}
	}
	seen := make(map[string]bool, len(c.Sources))
	for _, s := range c.Sources {
		if utf8.RuneCountInString(s.Name) > 100 {
			return fmt.Errorf("The name is at most 100 characters.")
		}
		key, err := SourceKey(s.URL)
		if err != nil {
			return fmt.Errorf("Enter an http(s) address.")
		}
		if seen[key] {
			return fmt.Errorf("This source is already in the list.")
		}
		seen[key] = true
	}
	return nil
}

func DefaultsYAML() string {
	c := Defaults()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		panic("config: encoding defaults: " + err.Error())
	}
	_ = enc.Close()
	return buf.String()
}
