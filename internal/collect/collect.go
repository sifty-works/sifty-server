package collect

import (
	"log/slog"
	"sync"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type Collector struct {
	store   *config.Store
	catalog *catalog.Catalog
	runner  *runner.Runner
	fetcher fetch.Fetcher
	db      *db.DB
	blocked bool
	now     func() time.Time
	log     *slog.Logger

	OnTick func(now time.Time)

	mu      sync.Mutex
	written int64
}

func New(
	store *config.Store,
	cat *catalog.Catalog,
	r *runner.Runner,
	f fetch.Fetcher,
	database *db.DB,
	blocked bool,
	now func() time.Time,
	log *slog.Logger,
) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{
		store:   store,
		catalog: cat,
		runner:  r,
		fetcher: f,
		db:      database,
		blocked: blocked,
		now:     now,
		log:     log,
	}
}

func (c *Collector) Register() {
	c.runner.Register(runner.Kind{Name: "collect", Run: c.run, Resume: false, Then: []string{"recompute_fit"}})
}
