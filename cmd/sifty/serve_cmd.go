package main

import (
	"context"
	"errors"
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
	"github.com/sifty-works/sifty-server/extension"
	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/locate"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
	"github.com/sifty-works/sifty-server/internal/shortlistcheck"
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

	settingsPath := filepath.Join(dataDir, settingsFileName)
	store, cfgResult, err := config.Open(settingsPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}
	cfg := store.Get()

	secrets, secretsErr := models.OpenSecrets(filepath.Join(dataDir, "secrets.env"))
	keys := heldKeys(store, secrets)
	scrubErr := scrubLogs(filepath.Join(dataDir, logDir), keys())

	logger, setLogKeys, logClose, err := openLog(dataDir, stderr)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer logClose()
	setLogKeys(keys)

	if scrubErr != nil {
		logger.Warn("old log lines not scrubbed", "err", scrubErr)
	}
	if secretsErr != nil {
		logger.Warn("secrets.env load failed", "err", secretsErr)
	}
	if cfgResult.SaveErr != nil {
		logger.Warn("restored settings not written", "err", cfgResult.SaveErr)
	}
	recovered := []server.RecoveredFile{}
	if cfgResult.Err != "" {
		recovered = append(recovered, server.RecoveredFile{File: "sifty.yaml", Text: settingsRecoveryText(cfgResult)})
	}
	if len(cfgResult.Unknown) > 0 {
		recovered = append(recovered, server.RecoveredFile{File: "sifty.yaml", Text: "Your settings file has settings this version does not use; they were ignored: " + strings.Join(cfgResult.Unknown, ", ") + "."})
	}

	if brokenSite, siteErr := source.LoadSites(filepath.Join(dataDir, "vocab", "sites.yaml")); siteErr != nil {
		recovered = append(recovered, server.RecoveredFile{File: "sites.yaml", Text: sitesRecoveryText(brokenSite, siteErr)})
	}

	templates, brokenForms, formsErr := formread.LoadTemplates(filepath.Join(dataDir, "vocab", "forms.yaml"))
	if formsErr != nil {
		recovered = append(recovered, server.RecoveredFile{File: "forms.yaml", Text: formsRecoveryText(brokenForms, formsErr)})
	}

	for _, r := range recovered {
		fmt.Fprintf(stderr, "recovered %s: %s\n", r.File, r.Text)
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

	gate := models.NewGate(database, store, offline, secrets, logger)
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
	r := runner.New(database, map[string]int{"net": 4, "cpu": 1}, time.Now, logger)
	r.SetPause(gate.IsPaused)

	registerJobKinds(r, database, store, gate, cat, fetcher, logger, offline, time.Now, formread.NewReader(fetcher, templates))
	queueUnsearched(ctx, database, r, logger)
	if n, err := candidate.ResetInterruptedAnalyses(ctx, database); err != nil {
		logger.Warn("interrupted analyses not reset", "err", err)
	} else if n > 0 {
		logger.Info("interrupted analyses set back to ready", "count", n)
	}
	rules := fitRules(store)
	rd := &reading{database: database, store: store, runner: r, ready: gate.Ready}
	recomputeFit := enqueueRecomputeFit(database, r, logger, rd)
	recomputeFit()

	blocked := offline && flags.replayDir == ""
	col := collect.New(store, cat, r, fetcher, database, blocked, nil, logger)
	col.Follow = func(ctx context.Context) error {
		return errors.Join(queueAnalysis(ctx, database, r), rd.queue(ctx))
	}
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

	filesDir := filepath.Join(dataDir, filesDirName)

	extensionOrigin, err := extension.Origin()
	if err != nil {
		return err
	}

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
		ExtensionOrigin:   extensionOrigin,
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
		registerAllRoutes(mux, logger, gate, database, time.Now, collectActive, sourceNames, rules, recomputeFit, filesDir, candidate.Readers{PDF: candidate.ProgramText{Program: "pdftotext", Args: []string{"-", "-"}}, Pages: fetcher}, col, r, gate.Ready, filepath.Join(dataDir, logDir, logFileName), keys, rd)
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

func settingsRecoveryText(r config.OpenResult) string {
	used := "The default settings are used"
	if r.Backup != "" {
		used = "The settings of the backup backups/" + r.Backup + " are used"
	}
	if r.Broken == "" {
		return "Your settings file could not be used and could not be moved aside, so it was left as it is. " + r.Err + " " + used + " for this run."
	}
	switch {
	case r.SaveErr != nil:
		used += " for this run; they could not be written, so set them again in Settings."
	case r.Backup != "":
		used = "The settings were restored from the backup backups/" + r.Backup + "."
	default:
		used += "."
	}
	return "Your settings file could not be used and was moved to " + r.Broken + ". " + r.Err + " " + used
}

func sitesRecoveryText(broken string, err error) string {
	var refused source.SitesRefused
	return vocabRecoveryText("site descriptions", "sites.yaml", broken, errors.As(err, &refused), refused.Reason)
}

func formsRecoveryText(broken string, err error) string {
	var refused formread.Refused
	return vocabRecoveryText("form templates", "forms.yaml", broken, errors.As(err, &refused), refused.Reason)
}

func vocabRecoveryText(what, file, broken string, refused bool, reason string) string {
	prefix, builtIn := "Your "+what+" file vocab/"+file+" ", " The built-in "+what+" are used"
	switch {
	case !refused:
		return prefix + "could not be read." + builtIn + "."
	case broken == "":
		return prefix + "could not be used and could not be moved aside, so it was left as it is. " + reason + builtIn + " for this run."
	}
	return prefix + "could not be used and was moved to " + broken + ". " + reason + builtIn + "."
}

func openLog(dataDir string, stderr io.Writer) (*slog.Logger, func(func() []string), func(), error) {
	logsDir := filepath.Join(dataDir, logDir)
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return nil, nil, nil, err
	}

	logPath := filepath.Join(logsDir, logFileName)

	if info, err := os.Stat(logPath); err == nil && info.Size() > logMaxBytes {
		rotated := filepath.Join(logsDir, logFileName+".1")
		os.Rename(logPath, rotated)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, nil, err
	}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	mask := &keyMask{}
	logger := slog.New(slog.NewMultiHandler(slog.NewTextHandler(maskedWriter{stderr, mask}, opts), slog.NewJSONHandler(maskedWriter{logFile, mask}, opts)))

	return logger, mask.setKeys, func() { logFile.Close() }, nil
}

func runRetention(ctx context.Context, j *runner.Job, cat *catalog.Catalog, database *db.DB, log *slog.Logger, now time.Time, maxAgeMonths int) error {
	var oldest time.Time
	if maxAgeMonths > 0 {
		oldest = now.AddDate(0, -maxAgeMonths, 0)
	}
	ids, err := cat.Removable(ctx, now.UTC().Add(-24*time.Hour), oldest)
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
	log.Info("retention: removed expired and old undecided vacancies", "count", len(ids))

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

func registerJobKinds(r *runner.Runner, database *db.DB, store *config.Store, gate modelSource, cat *catalog.Catalog, fetcher fetch.Fetcher, logger *slog.Logger, offline bool, now func() time.Time, forms formread.FormReader) {
	r.Register(runner.Kind{
		Name:   "recompute_fit",
		Resume: true,
		After:  []string{analysis.Kind},
		Run: func(runCtx context.Context, j *runner.Job) error {
			rel, err := j.Lane(runCtx, "cpu")
			if err != nil {
				return err
			}
			defer rel()
			return fit.Recompute(runCtx, database, fitRules(store), j.Progress)
		},
	})

	r.Register(runner.Kind{
		Name:   analysis.Kind,
		Resume: true,
		Serial: true,
		Then:   []string{"recompute_fit"},
		Run:    analysis.Job(database, loadForAnalysis(database)),
	})

	r.Register(runner.Kind{
		Name:    analysis.ReadKind,
		Resume:  true,
		Serial:  true,
		Patient: true,
		Then:    []string{"recompute_fit"},
		Run: analysis.ReadJob(database, loadForAnalysis(database),
			func(class string, vid int64) models.Model {
				return gate.For(models.NamedTask("read_domain").Work, class, vid)
			},
			func() bool { return store.Get().PreciseReadingOn() }),
	})

	r.Register(runner.Kind{
		Name:   shortlistcheck.Kind,
		Resume: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runShortlistCheck(runCtx, j, database,
				func(vid int64) models.Model {
					return gate.For(models.NamedTask("shortlist_check").Work, "background", vid)
				}, now)
		},
	})

	r.Register(runner.Kind{
		Name:   locate.Kind,
		Resume: true,
		Run:    locate.Run(database, locate.Finder{Fetcher: fetcher, Boards: boardLists{fetcher}}, now),
		Follow: func(runCtx context.Context, j *runner.Job, enqueue func(runner.Spec) (int64, error)) error {
			return queuePrepare(runCtx, database, enqueue, j.Subject)
		},
	})

	r.Register(runner.Kind{
		Name:    "analyze_document",
		Resume:  false,
		Patient: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runAnalyzeDocument(runCtx, j, database, gate.For(models.NamedTask("document_proposals").Work, "background"))
		},
	})

	r.Register(runner.Kind{
		Name: formread.Kind,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runPrepare(runCtx, j, database, forms, now)
		},
	})

	r.Register(runner.Kind{
		Name:   "retention",
		Resume: true,
		Run: func(runCtx context.Context, j *runner.Job) error {
			return runRetention(runCtx, j, cat, database, logger, now(), store.Get().Collection.MaxAgeMonths)
		},
	})
}
