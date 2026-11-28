package websearch

import "context"

type Result struct {
	Title string
	URL   string
}

type WebSearch interface {
	Search(ctx context.Context, q string) ([]Result, error)
}
