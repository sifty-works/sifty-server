package facts

type Kind string

const (
	Money       Kind = "money"
	WorkMode    Kind = "work_mode"
	Engagement  Kind = "engagement"
	Place       Kind = "place"
	TimeZone    Kind = "time_zone"
	Hours       Kind = "hours"
	Language    Kind = "language"
	Requirement Kind = "requirement"
	Offer       Kind = "offer"
	Analysed    Kind = "analysed"
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
