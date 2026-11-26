package analysis

import (
	"context"

	"github.com/sifty-works/sifty-server/internal/facts"
)

type Section struct {
	Name       string
	Start, End int
}

type Doc struct {
	Original string
	Sections []Section
	Facts    []facts.Fact
}

type Worker interface {
	Name() string
	Version() int
	Produces() []facts.Kind
	Extract(ctx context.Context, doc *Doc) ([]facts.Fact, error)
}

var Workers = []Worker{}
