package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func TestRunningJobsAfterRestartResumeExceptDocumentAnalysis(t *testing.T) {
	cases := []struct {
		kind, subject, want string
	}{
		{"recompute_fit", "", "queued"},
		{"analyze_vacancy", "1/1", "queued"},
		{"shortlist_check", "1/1", "queued"},
		{"retention", "", "queued"},
		{"analyze_document", "1", "interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			d := openTestDB(t)
			ts := time.Now().UTC().Format(time.RFC3339)
			if _, err := d.Exec(
				`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
				 VALUES (?,?,0,'running',1,?,'{}','',NULL,?,?)`,
				tc.kind, tc.subject, ts, ts, ts,
			); err != nil {
				t.Fatalf("insert running job: %v", err)
			}
			r := runner.New(d, nil, time.Now, slog.Default())
			registerKindsWith(t, r, d, &models.FakeModel{})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r.Start(ctx)

			var state string
			d.QueryRow(`SELECT state FROM job WHERE kind=?`, tc.kind).Scan(&state)
			if state != tc.want {
				t.Errorf("%s after restart: state = %q, want %q", tc.kind, state, tc.want)
			}
		})
	}
}
