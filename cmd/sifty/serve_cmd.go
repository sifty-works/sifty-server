package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	siftyapi "github.com/sifty-works/sifty-server/api"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
	"github.com/sifty-works/sifty-server/internal/source"
	"github.com/sifty-works/sifty-server/web"
)

const (
	logMaxBytes  = 10 << 20
	lockFileName = "sifty.lock"
	logDir       = "logs"
	logFileName  = "sifty.log"
	dbFileName   = "sifty.db"
)

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags, err := parseServeFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}

	dataDir, err := filepath.Abs(resolveDataDir(flags.dataDir))
	if err != nil {
		return fmt.Errorf("resolving data dir: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("creating data dir %s: %w", dataDir, err)
	}

	lockPath := filepath.Join(dataDir, lockFileName)
	lockFile, err := acquireLock(lockPath)
	if err != nil {
		fmt.Fprintf(stderr, "Another Sifty is already running on this data folder (%s)\n", dataDir)
		return fmt.Errorf("lock: %w", err)
	}
	defer lockFile.Close()

	logger, logClose, err := openLog(dataDir, stderr)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer logClose()

	settingsPath := filepath.Join(dataDir, "sifty.yaml")
	store, cfgResult, err := config.Open(settingsPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}
	cfg := store.Get()

	recovered := []server.RecoveredFile{}
	if cfgResult.Broken != "" {
		recovered = append(recovered, server.RecoveredFile{File: "sifty.yaml", Err: cfgResult.Err, Broken: cfgResult.Broken})
	}
	if len(cfgResult.Unknown) > 0 {
		recovered = append(recovered, server.RecoveredFile{File: "sifty.yaml", Err: "ignored unknown keys: " + strings.Join(cfgResult.Unknown, ", ")})
	}

	if brokenSite, siteErr := source.LoadSites(filepath.Join(dataDir, "vocab", "sites.yaml")); siteErr != nil {
		recovered = append(recovered, server.RecoveredFile{File: "sites.yaml", Err: siteErr.Error(), Broken: brokenSite})
	}

	for _, r := range recovered {
		if r.Broken != "" {
			fmt.Fprintf(stderr, "recovered %s: %s (moved to %s)\n", r.File, r.Err, r.Broken)
		} else {
			fmt.Fprintf(stderr, "recovered %s: %s\n", r.File, r.Err)
		}
	}

	if flags.port >= 0 {
		cfg.Port = flags.port
	}
	if flags.debug {
		cfg.Debug = true
	}
	offline := flags.offline

	dbPath := filepath.Join(dataDir, dbFileName)
	database, err := db.Open(dbPath, db.Migrations)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}
	defer database.Close()

	secretsPath := filepath.Join(dataDir, "secrets.env")
	secrets, err := models.LoadSecrets(secretsPath)
	if err != nil {
		logger.Warn("secrets.env load failed", "err", err)
		secrets = map[string]string{}
	}
	gate := models.NewGate(database, store, offline, secrets)
	defer gate.Stop(context.Background())

	var fetcher fetch.Fetcher
	if flags.replayDir != "" {
		rm, err := fetchtest.Load(flags.replayDir)
		if err != nil {
			return fmt.Errorf("loading replay dir: %w", err)
		}
		rm.OnMiss = func(url string) { logger.Info("replay: not recorded " + url) }
		fetcher = rm
	} else {
		fetcher = fetch.New(fetch.Options{
			Blocks:  fetch.NewDomainBlocks(database),
			Offline: offline,
		})
	}

	cat := catalog.New(database, time.Now)
	r := runner.New(database, map[string]int{"browser": 1, "net": 4, "cpu": 1}, time.Now, logger)

	registerJobKinds(r, database, store, gate, cat, logger, offline, time.Now)
	if n, err := candidate.ResetInterruptedAnalyses(ctx, database); err != nil {
		logger.Warn("interrupted analyses not reset", "err", err)
	} else if n > 0 {
		logger.Info("interrupted analyses set back to ready", "count", n)
	}
	rules := fitRules(store)
	recomputeFit := enqueueRecomputeFit(r)
	recomputeFit()

	blocked := offline && flags.replayDir == ""
	col := collect.New(store, cat, r, fetcher, database, blocked, nil, logger)
	col.Register()

	col.OnTick = func(now time.Time) {
		if retentionDue(now, r) {
			r.Enqueue(runner.Spec{Kind: "retention"})
		}
	}

	if retentionDue(time.Now(), r) {
		r.Enqueue(runner.Spec{Kind: "retention"})
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	collectActive := func() bool {
		return len(r.Active("collect")) > 0
	}

	filesDir := filepath.Join(dataDir, "files")

	handler := server.New(server.Options{
		Version:           version,
		APISha256:         siftyapi.Sha256(),
		Port:              port,
		Offline:           offline,
		Debug:             cfg.Debug,
		Logger:            logger,
		Static:            web.StaticFS(),
		Status:            col.StatusFunc(),
		ModelsStatus:      gate.Status,
		BinaryUploadPaths: []string{"/api/me/photo", "/api/documents"},
		Recovered:         recovered,
	}, func(mux *http.ServeMux) {
		sourceNames := func() map[string]string {
			cfg := store.Get()
			m := make(map[string]string, len(cfg.Sources))
			for _, s := range cfg.Sources {
				if key, err := config.SourceKey(s.URL); err == nil {
					m[key] = s.Name
				}
			}
			return m
		}
		registerAllRoutes(mux, logger, gate, database, time.Now, collectActive, sourceNames, rules, recomputeFit, filesDir, candidate.ProgramText{Program: "pdftotext", Args: []string{"-", "-"}}, col, r)
	})

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	listenMsg := fmt.Sprintf("Listening on http://127.0.0.1:%d", port)
	logger.Info(listenMsg)
	fmt.Fprintln(stdout, listenMsg)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go r.Start(bgCtx)
	go col.Ticker(bgCtx)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		bgCancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		return srv.Shutdown(shutCtx)
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func openLog(dataDir string, stderr io.Writer) (*slog.Logger, func(), error) {
	logsDir := filepath.Join(dataDir, logDir)
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(logsDir, logFileName)

	if info, err := os.Stat(logPath); err == nil && info.Size() > logMaxBytes {
		rotated := filepath.Join(logsDir, logFileName+".1")
		os.Rename(logPath, rotated)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, err
	}

	tee := io.MultiWriter(stderr, logFile)
	handler := slog.NewTextHandler(tee, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	return logger, func() { logFile.Close() }, nil
}

func runRetention(ctx context.Context, j *runner.Job, cat *catalog.Catalog, database *db.DB, log *slog.Logger, now time.Time) error {
	cutoff := now.UTC().Add(-24 * time.Hour)

	ids, err := cat.RetainableExpired(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("retention: load: %w", err)
	}
	j.Progress(0, len(ids), "")

	for done, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := cat.Delete(ctx, id); err != nil {
			return fmt.Errorf("retention: delete %d: %w", id, err)
		}
		j.Progress(done+1, len(ids), "")
	}
	log.Info("retention: removed expired vacancies", "count", len(ids))

	modelCallCutoff := now.UTC().Add(-90 * 24 * time.Hour)
	if _, err := database.ExecContext(ctx, `DELETE FROM model_call WHERE created_at < ?`, modelCallCutoff.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("retention: model_call: %w", err)
	}
	return nil
}

func retentionDue(now time.Time, r *runner.Runner) bool {
	last, ok := r.Last("retention")
	if !ok {
		return true
	}
	return now.After(last.Created.Add(24 * time.Hour))
}

type modelSource interface {
	For(kind, class string, vacancy ...int64) models.Model
}

func fitRules(store *config.Store) func() fit.Rules {
	return func() fit.Rules { c := store.Get(); return fit.Rules{Weights: c.Weights, Rates: c.Rates} }
}

func registerJobKinds(r *runner.Runner, database *db.DB, store *config.Store, gate modelSource, cat *catalog.Catalog, logger *slog.Logger, offline bool, now func() time.Time) {
	r.Register(runner.Kind{
		Name:   "recompute_fit",
		Resume: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			rel, err := j.Lane(runCtx, "cpu")
			if err != nil {
				return err
			}
			defer rel()
			if err := fit.Recompute(runCtx, database, fitRules(store), j.Progress); err != nil {
				return err
			}
			return queueReadings(runCtx, database, r.Enqueue, store, offline, reading.ReaderVersion())
		},
	})

	r.Register(runner.Kind{
		Name:   "read_vacancy",
		Resume: true,
		Serial: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runReadVacancy(runCtx, j, database,
				func(vid int64) models.Model { return gate.For("reading", "background", vid) },
				r.Enqueue)
		},
	})

	r.Register(runner.Kind{
		Name:   "analyze_document",
		Resume: false,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runAnalyzeDocument(runCtx, j, database, gate.For("reading", "background"))
		},
	})

	r.Register(runner.Kind{
		Name:   "retention",
		Resume: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runRetention(runCtx, j, cat, database, logger, now())
		},
	})
}
