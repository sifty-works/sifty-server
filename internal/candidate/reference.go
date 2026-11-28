package candidate

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

//go:embed options.json
var optionsJSON []byte

type OptionItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ReferenceOptions struct {
	SkillLevels       []OptionItem `json:"skillLevels"`
	LanguageLevels    []OptionItem `json:"languageLevels"`
	Currencies        []OptionItem `json:"currencies"`
	Periods           []OptionItem `json:"periods"`
	PaymentMethods    []OptionItem `json:"paymentMethods"`
	Engagement        []OptionItem `json:"engagement"`
	WorkingTime       []OptionItem `json:"workingTime"`
	WorkModes         []OptionItem `json:"workModes"`
	Domains           []OptionItem `json:"domains"`
	Relocation        []OptionItem `json:"relocation"`
	NoticeUnits       []OptionItem `json:"noticeUnits"`
	Gender            []OptionItem `json:"gender"`
	Race              []OptionItem `json:"race"`
	Ethnicity         []OptionItem `json:"ethnicity"`
	Disability        []OptionItem `json:"disability"`
	VeteranStatus     []OptionItem `json:"veteranStatus"`
	CriminalRecord    []OptionItem `json:"criminalRecord"`
	WorkAuthorization []OptionItem `json:"workAuthorization"`
}

type ReferenceResp struct {
	Countries     []places.ReferenceCountry      `json:"countries"`
	Regions       []places.ReferenceRegion       `json:"regions"`
	TimeZoneBands []places.ReferenceTimeZoneBand `json:"timeZoneBands"`
	Options       ReferenceOptions               `json:"options"`
}

var referenceData = mustLoadReference()

func mustLoadReference() ReferenceResp {
	r := ReferenceResp{Countries: places.Countries(), Regions: places.Regions(), TimeZoneBands: places.Bands()}
	if err := json.Unmarshal(optionsJSON, &r); err != nil {
		panic(fmt.Sprintf("candidate: load options.json: %v", err))
	}
	for _, d := range facts.Domains {
		r.Options.Domains = append(r.Options.Domains, OptionItem{Value: d.Name, Label: d.Label})
	}
	return r
}

func domainKeys() []string {
	keys := make([]string, len(facts.Domains))
	for i, d := range facts.Domains {
		keys[i] = d.Name
	}
	return keys
}

func Options() ReferenceOptions { return referenceData.Options }
