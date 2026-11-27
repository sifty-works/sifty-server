package collect

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

type SourceStatus struct {
	Text      string `json:"text"`
	Attention bool   `json:"attention"`
}

type Source struct {
	Key     string        `json:"key"`
	Name    string        `json:"name"`
	URL     string        `json:"url"`
	Enabled bool          `json:"enabled"`
	Status  *SourceStatus `json:"status,omitempty"`
}

type SourcesResp struct {
	Sources      []Source `json:"sources"`
	Auto         bool     `json:"auto"`
	Interval     string   `json:"interval"`
	MaxAgeMonths *int     `json:"max_age_months"`
}

type addSourceReq struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type toggleSourceReq struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

type removeSourceReq struct {
	Key string `json:"key"`
}

type scheduleReq struct {
	Auto         bool     `json:"auto"`
	Interval     string   `json:"interval"`
	MaxAgeMonths *float64 `json:"max_age_months"`
}

type collectReq struct {
	Force bool `json:"force"`
}

func (c *Collector) StatusFunc() func() server.StatusExtra {
	return c.buildStatusExtra
}

func (c *Collector) buildStatusExtra() server.StatusExtra {
	var collecting *server.CollectProgress
	for _, j := range c.runner.Active("collect") {
		text := "Collecting…"
		if j.Total > 0 && j.Done < j.Total {
			text = fmt.Sprintf("Collecting… %d of %d sources", j.Done+1, j.Total)
		}
		collecting = &server.CollectProgress{Done: j.Done, Total: j.Total, Text: text}
		break
	}
	var lastRun *server.LastRun
	if j, ok := c.runner.Last("collect"); ok {
		switch j.State {
		case "done", "failed", "interrupted":
			msg := ""
			if j.State == "failed" {
				msg = j.Error
			}
			lastRun = &server.LastRun{
				State:    j.State,
				Finished: j.Updated.UTC().Format(time.RFC3339),
				Message:  msg,
			}
		}
	}
	c.mu.Lock()
	written := c.written
	c.mu.Unlock()

	cfg := c.store.Get()
	needsAttn := c.attentionSources()
	var attentionCount int
	for _, s := range cfg.Sources {
		if !s.Enabled {
			continue
		}
		key, _ := config.SourceKey(s.URL)
		if needsAttn[key] {
			attentionCount++
		}
	}
	var attention *server.Attention
	if attentionCount > 0 {
		noun := "sources need"
		if attentionCount == 1 {
			noun = "source needs"
		}
		attention = &server.Attention{
			Count: attentionCount,
			Text:  strconv.Itoa(attentionCount) + " " + noun + " attention",
		}
	}

	return server.StatusExtra{
		Collecting: collecting,
		LastRun:    lastRun,
		Catalogue:  written,
		Attention:  attention,
	}
}

func (c *Collector) updateSources(update func(*config.Config) error) (SourcesResp, error) {
	if err := c.store.Update(update); err != nil {
		return SourcesResp{}, err
	}
	return c.buildSourcesResp(), nil
}

func (c *Collector) RegisterRoutes(mux *http.ServeMux) {
	server.Handle(mux, "GET /api/sources", c.log, func(r *http.Request, _ struct{}) (SourcesResp, error) {
		return c.buildSourcesResp(), nil
	})

	server.Handle(mux, "POST /api/sources", c.log, func(r *http.Request, req addSourceReq) (SourcesResp, error) {
		return c.updateSources(func(cfg *config.Config) error {
			cfg.Sources = append(cfg.Sources, config.SourceConfig{
				Name:    req.Name,
				URL:     req.URL,
				Enabled: true,
			})
			return nil
		})
	})

	server.Handle(mux, "PATCH /api/sources", c.log, func(r *http.Request, req toggleSourceReq) (SourcesResp, error) {
		return c.updateSources(func(cfg *config.Config) error {
			for i := range cfg.Sources {
				key, _ := config.SourceKey(cfg.Sources[i].URL)
				if key == req.Key {
					cfg.Sources[i].Enabled = req.Enabled
					return nil
				}
			}
			return apperr.NotFound{Message: "This source is no longer in the list. Refresh the page."}
		})
	})

	server.Handle(mux, "DELETE /api/sources", c.log, func(r *http.Request, req removeSourceReq) (SourcesResp, error) {
		return c.updateSources(func(cfg *config.Config) error {
			for i, s := range cfg.Sources {
				key, _ := config.SourceKey(s.URL)
				if key == req.Key {
					cfg.Sources = append(cfg.Sources[:i], cfg.Sources[i+1:]...)
					return nil
				}
			}
			return apperr.NotFound{Message: "This source is no longer in the list. Refresh the page."}
		})
	})

	server.Handle(mux, "PUT /api/sources/schedule", c.log, func(r *http.Request, req scheduleReq) (SourcesResp, error) {
		d, err := time.ParseDuration(req.Interval)
		if err != nil {
			return SourcesResp{}, apperr.Invalid{Message: config.IntervalRefusal}
		}
		months, err := config.AgeLimit(req.MaxAgeMonths)
		if err != nil {
			return SourcesResp{}, apperr.Invalid{Message: err.Error()}
		}
		return c.updateSources(func(cfg *config.Config) error {
			cfg.Collection.Auto = req.Auto
			cfg.Collection.Interval = config.Duration(d)
			cfg.Collection.MaxAgeMonths = months
			return nil
		})
	})

	server.Handle(mux, "POST /api/collect", c.log, func(r *http.Request, req collectReq) (server.StatusExtra, error) {
		if req.Force || c.scheduled(r.Context(), c.now()) {
			c.runner.Enqueue(runner.Spec{Kind: "collect"})
		}
		return c.buildStatusExtra(), nil
	})
}

func (c *Collector) buildSourcesResp() SourcesResp {
	cfg := c.store.Get()
	sources := make([]Source, len(cfg.Sources))
	for i, s := range cfg.Sources {
		name := s.Name
		if name == "" {
			if u, err := url.Parse(s.URL); err == nil {
				name = u.Host
			}
		}
		key, _ := config.SourceKey(s.URL)
		sources[i] = Source{
			Key:     key,
			Name:    name,
			URL:     s.URL,
			Enabled: s.Enabled,
			Status:  c.buildSourceStatus(key),
		}
	}
	var maxAge *int
	if m := cfg.Collection.MaxAgeMonths; m > 0 {
		maxAge = &m
	}
	return SourcesResp{
		Sources:      sources,
		Auto:         cfg.Collection.Auto,
		Interval:     cfg.Collection.Interval.String(),
		MaxAgeMonths: maxAge,
	}
}

func (c *Collector) buildSourceStatus(key string) *SourceStatus {
	history := c.querySourceRuns(key)
	if len(history) == 0 {
		return nil
	}
	last := history[0]

	var lastSuccess *sourceRunRec
	for i := range history {
		if history[i].status == "ok" {
			lastSuccess = &history[i]
			break
		}
	}

	now := c.now()
	thirtyDaysAgo := now.Add(-30 * 24 * time.Hour)
	firstOld := !history[len(history)-1].started.After(thirtyDaysAgo)
	noRecentVacancies := true
	for _, r := range history {
		if !r.started.Before(thirtyDaysAgo) && r.status == "ok" {
			noRecentVacancies = false
			break
		}
	}

	var text string
	switch {
	case last.status == "error":
		text = last.errText
		if lastSuccess != nil {
			text += " · last success " + lastSuccess.started.Local().Format("2 Jan 15:04")
		}
	case firstOld && noRecentVacancies:
		text = "No vacancies in 30 days"
	case lastSuccess != nil:
		text = "Last success " + lastSuccess.started.Local().Format("2 Jan 15:04") +
			" · " + strconv.Itoa(lastSuccess.found) + " seen · " +
			strconv.Itoa(lastSuccess.newCnt) + " new"
		if lastSuccess.refused > 0 {
			text += " · " + strconv.Itoa(lastSuccess.refused) + " without an employer"
		}
	}

	return &SourceStatus{Text: text, Attention: needsAttention(history)}
}
