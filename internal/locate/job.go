package locate

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const Kind = "locate"

func Run(database *db.DB, finder Locator, now func() time.Time) func(context.Context, *runner.Job) error {
	return func(ctx context.Context, j *runner.Job) error {
		id, err := strconv.ParseInt(j.Subject, 10, 64)
		if err != nil {
			return runner.Permanent(err)
		}
		v, found, exists, err := load(ctx, database, id)
		if err != nil || found || !exists {
			return err
		}
		rel, err := j.Lane(ctx, "net")
		if err != nil {
			return err
		}
		defer rel()
		link, err := finder.Locate(ctx, v, false)
		if errors.Is(err, fetch.ErrOffline) {
			return runner.Permanent(err)
		}
		if err != nil {
			return err
		}
		return save(ctx, database, id, link, now())
	}
}
