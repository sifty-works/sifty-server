package facts

type Kind string

const (
	Money        Kind = "money"
	WorkMode     Kind = "work_mode"
	Engagement   Kind = "engagement"
	Place        Kind = "place"
	TimeZone     Kind = "time_zone"
	Hours        Kind = "hours"
	Language     Kind = "language"
	Requirement  Kind = "requirement"
	Years        Kind = "years"
	Seniority    Kind = "seniority"
	CompanyStage Kind = "company_stage"
	Deadline     Kind = "deadline"
	ContractLen  Kind = "contract_length"
	Clearance    Kind = "clearance"
	Relocation   Kind = "relocation"
	Visa         Kind = "visa"
	Domain       Kind = "domain"
	Analysed     Kind = "analysed"
)

type Flag string

const (
	Required   Flag = "required"
	Preferred  Flag = "preferred"
	Irrelevant Flag = "irrelevant"
)

type Way string

const (
	SiteField  Way = "site_field"
	Vocabulary Way = "vocabulary"
	Model      Way = "model"
)

type Origin string

const (
	Aggregator Origin = "aggregator"
	Employer   Origin = "employer"
)

type Source struct {
	Way    Way
	Origin Origin
}

type Fact struct {
	Vacancy       int64
	Version       int64
	Worker        string
	Kind          Kind
	Value         string
	Flag          Flag
	Start, End    int
	Source        Source
	WorkerVersion int
}

type DomainLabel struct{ Name, Label string }

var Domains = []DomainLabel{
	{"infrastructure", "Infrastructure"}, {"defi", "DeFi"}, {"exchange", "Exchange"}, {"wallet", "Wallet & custody"},
	{"payments", "Payments"}, {"fintech", "Fintech"}, {"ai", "AI"}, {"security", "Security"}, {"data", "Data"},
	{"gaming", "Gaming"}, {"gambling", "Gambling"}, {"prediction_markets", "Prediction markets"}, {"adtech", "Adtech"},
	{"ecommerce", "E-commerce"}, {"health", "Health"}, {"education", "Education"}, {"government", "Government & defence"},
	{"media", "Media"}, {"nft", "NFT"}, {"saas", "SaaS"}, {"hardware", "Hardware"}, {"other", "Other"},
}

func DomainTitle(name string) string {
	for _, d := range Domains {
		if d.Name == name {
			return d.Label
		}
	}
	return ""
}
