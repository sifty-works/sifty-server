package locate

import "context"

type Vacancy struct {
	Title   string
	Company string
}

type Link struct {
	URL    string
	System string
}

type Job struct {
	Title string
	URL   string
}

type Boards interface {
	Jobs(ctx context.Context, system, board string) ([]Job, error)
}

type Locator interface {
	Locate(ctx context.Context, v Vacancy, deep bool) (Link, error)
}
