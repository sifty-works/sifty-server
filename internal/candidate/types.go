package candidate

type MeData struct {
	DisplayName        string              `json:"displayName,omitempty"`
	LegalName          string              `json:"legalName,omitempty"`
	Email              string              `json:"email,omitempty"`
	Phone              string              `json:"phone,omitempty"`
	LinkedIn           string              `json:"linkedin,omitempty"`
	GitHub             string              `json:"github,omitempty"`
	X                  string              `json:"x,omitempty"`
	Website            string              `json:"website,omitempty"`
	Skills             []Skill             `json:"skills,omitempty"`
	AbsentSkills       []AbsentSkill       `json:"absentSkills,omitempty"`
	ConfirmedFacts     []ConfirmedFact     `json:"confirmedFacts,omitempty"`
	Experience         []Experience        `json:"experience,omitempty"`
	Languages          []Language          `json:"languages,omitempty"`
	Availability       *Availability       `json:"availability,omitempty"`
	CurrentLocation    *Location           `json:"currentLocation,omitempty"`
	WorkAuthorizations []WorkAuthorization `json:"workAuthorizations,omitempty"`
	Demographics       *Demographics       `json:"demographics,omitempty"`
}

type Skill struct {
	Name   string `json:"name"`
	Level  string `json:"level,omitempty"`
	Source string `json:"source,omitempty"`
}

type AbsentSkill struct {
	Name string `json:"name"`
}

type ConfirmedFact struct {
	Text string `json:"text"`
	Have bool   `json:"have"`
}

type Experience struct {
	Company string   `json:"company"`
	Title   string   `json:"title"`
	Start   string   `json:"start,omitempty"`
	End     string   `json:"end,omitempty"`
	Current bool     `json:"current,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Skills  []string `json:"skills,omitempty"`
}

type Language struct {
	Name  string `json:"name"`
	Level string `json:"level,omitempty"`
}

type Availability struct {
	NoticeValue *int   `json:"noticeValue"`
	NoticeUnit  string `json:"noticeUnit,omitempty"`
	StartDate   string `json:"startDate,omitempty"`
}

type Location struct {
	Country string `json:"country"`
	City    string `json:"city,omitempty"`
}

type WorkAuthorization struct {
	Country string `json:"country"`
	Status  string `json:"status,omitempty"`
}

type Demographics struct {
	Gender            string   `json:"gender,omitempty"`
	GenderDescription string   `json:"genderDescription,omitempty"`
	Race              []string `json:"race,omitempty"`
	RaceDescription   string   `json:"raceDescription,omitempty"`
	Ethnicity         string   `json:"ethnicity,omitempty"`
	Disability        string   `json:"disability,omitempty"`
	VeteranStatus     string   `json:"veteranStatus,omitempty"`
	CriminalRecord    string   `json:"criminalRecord,omitempty"`
}

type Compensation struct {
	CashMin     *float64  `json:"cashMin"`
	CashTarget  *float64  `json:"cashTarget"`
	Currency    string    `json:"currency,omitempty"`
	GrossNet    string    `json:"grossNet,omitempty"`
	Period      string    `json:"period,omitempty"`
	Crypto      string    `json:"crypto,omitempty"`
	EquityOnTop *bool     `json:"equityOnTop"`
	DayRates    *DayRates `json:"dayRates,omitempty"`
}

type DayRates struct {
	Min      *float64 `json:"min"`
	Target   *float64 `json:"target"`
	Currency string   `json:"currency,omitempty"`
}

type Hours struct {
	Weekly    *float64 `json:"weekly,omitempty"`
	WeeklyMin *float64 `json:"weeklyMin,omitempty"`
	WeeklyMax *float64 `json:"weeklyMax,omitempty"`
}
