package listing

import "time"

type TextState string

const (
	TextFull       TextState = "full"
	TextSummary    TextState = "summary"
	TextBlocked    TextState = "blocked"
	TextNotFetched TextState = "not_fetched"
	TextRemoved    TextState = "removed"
)

type Posting struct {
	URL            string
	ApplyURL       string
	Title          string
	Company        string
	CompanyGuessed bool
	Location       string
	Tags           []string
	Fields         map[string]string

	Published      time.Time
	PublishedBasis string
	Expires        time.Time

	Text      string
	TextState TextState

	Requested bool
}
