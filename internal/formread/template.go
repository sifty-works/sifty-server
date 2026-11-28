package formread

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"strings"

	"github.com/sifty-works/sifty-server/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed forms.yaml
var embeddedForms []byte

const kindSkip = "skip"

var decoders = map[string]func(string) (any, error){
	"json": func(s string) (any, error) {
		var v any
		err := json.Unmarshal([]byte(s), &v)
		return v, err
	},
}

type QuestionSet struct {
	List        string `yaml:"list"`
	Label       string `yaml:"label"`
	Required    string `yaml:"required"`
	Hidden      string `yaml:"hidden"`
	Type        string `yaml:"type"`
	Options     string `yaml:"options"`
	OptionLabel string `yaml:"option_label"`
	ID          string `yaml:"id"`
}

type Template struct {
	Hosts     []string          `yaml:"hosts"`
	System    string            `yaml:"system"`
	Page      string            `yaml:"page"`
	Data      string            `yaml:"data"`
	Decode    string            `yaml:"decode"`
	Kinds     map[string]string `yaml:"kinds"`
	Questions []QuestionSet     `yaml:"questions"`

	page *regexp.Regexp
}

type formsFile struct {
	Forms []Template `yaml:"forms"`
}

type Templates map[string]*Template

type Refused struct{ Reason string }

func (e Refused) Error() string { return e.Reason }

var groupRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func Embedded() Templates {
	t, err := parseTemplates(embeddedForms)
	if err != nil {
		panic("formread: cannot parse embedded forms.yaml: " + err.Error())
	}
	return t
}

func parseTemplates(data []byte) (Templates, error) {
	var f formsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, Refused{"It is not a valid form templates file."}
	}
	out := Templates{}
	for i := range f.Forms {
		t := &f.Forms[i]
		if len(t.Hosts) == 0 {
			return nil, Refused{"A form template has no host."}
		}
		for _, h := range t.Hosts {
			h = strings.ToLower(strings.TrimSpace(h))
			if out[h] != nil {
				return nil, Refused{fmt.Sprintf("The host %s is listed twice.", h)}
			}
			if err := t.validate(h); err != nil {
				return nil, err
			}
			out[h] = t
		}
	}
	return out, nil
}

func (t *Template) validate(host string) error {
	re, err := regexp.Compile(t.Page)
	if err != nil || t.Page == "" {
		return Refused{fmt.Sprintf("The host %s has a page pattern that is not a valid regular expression.", host)}
	}
	t.page = re
	rest := strings.TrimPrefix(strings.TrimPrefix(t.Data, "https://"), "http://")
	if rest == t.Data {
		return Refused{fmt.Sprintf("The host %s has a data address that is not an http or https address.", host)}
	}
	if authority := rest[:strings.IndexAny(rest+"/", "/?#")]; authority == "" || strings.ContainsAny(authority, "{}@") {
		return Refused{fmt.Sprintf("The host %s takes the host of its data address from the page's address; a template fixes the host.", host)}
	}
	if t.Decode != "" && decoders[t.Decode] == nil {
		return Refused{fmt.Sprintf("The host %s names an unknown decoder %s.", host, t.Decode)}
	}
	return nil
}

func LoadTemplates(overridePath string) (templates Templates, broken string, err error) {
	templates = Embedded()
	broken, err = config.LoadOverride(overridePath, func(data []byte) error {
		user, err := parseTemplates(data)
		maps.Copy(templates, user)
		return err
	})
	return templates, broken, err
}

func (ts Templates) match(page string) (*Template, string, bool) {
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", false
	}
	t := ts[strings.ToLower(u.Hostname())]
	if t == nil {
		return nil, "", false
	}
	m := t.page.FindStringSubmatch(u.RequestURI())
	if m == nil {
		return nil, "", false
	}
	data := groupRE.ReplaceAllStringFunc(t.Data, func(g string) string {
		i := t.page.SubexpIndex(g[1 : len(g)-1])
		if i < 0 {
			return ""
		}
		return strings.ReplaceAll(url.QueryEscape(m[i]), "+", "%20")
	})
	return t, data, true
}
