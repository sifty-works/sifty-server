package source

import (
	"context"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var srcWorkable Module = &workableSource{board{hosts: []string{"apply.workable.com"}, depth: 3, mid: "j"}}

type workableSource struct{ board }

func (s *workableSource) Name() string { return "workable" }

func (s *workableSource) List(context.Context, fetch.Fetcher, Site) ([]listing.Posting, error) {
	return nil, nil
}
