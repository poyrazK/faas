package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/releasehistory"
)

// releaseHistoryLister reads the release history; tests replace it.
var releaseHistoryLister = listReleaseHistory

func listReleaseHistory(ctx context.Context, host string, limit int) ([]releasehistory.Record, error) {
	pool, err := openPgPoolFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	return releasehistory.NewStore(pool).List(ctx, host, limit)
}

type releaseHistoryRow struct {
	ID                string     `json:"id"`
	Host              string     `json:"host"`
	Kind              string     `json:"kind"`
	ReleaseID         string     `json:"release_id"`
	CommitSHA         string     `json:"commit_sha,omitempty"`
	PreviousReleaseID string     `json:"previous_release_id,omitempty"`
	Services          []string   `json:"services,omitempty"`
	Actor             string     `json:"actor"`
	Status            string     `json:"status"`
	Detail            string     `json:"detail,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

// cmdReleaseHistory lists release activations on control-plane hosts, newest
// first (issue #574): what was running on a host at a given time, and who
// changed it. deployctl records every deploy and rollback; a rerun that found
// its release already active is not an activation and has no row.
func cmdReleaseHistory(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl release history [--host NAME] [--limit 20] [--json]", "release")
		return 0
	}
	fs := flag.NewFlagSet("release history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", "", "only this host (default: every host)")
	limit := fs.Int("limit", 20, "most rows to show, 1-1000")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 1000 {
		_, _ = fmt.Fprintln(os.Stderr, "gregalectl release history: --limit must be 1-1000 and positional arguments are not accepted")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	records, err := releaseHistoryLister(ctx, strings.TrimSpace(*host), *limit)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gregalectl release history: %v\n", err)
		return 3
	}
	rows := make([]releaseHistoryRow, 0, len(records))
	for _, r := range records {
		rows = append(rows, releaseHistoryRow{
			ID: r.ID, Host: r.Host, Kind: r.Kind, ReleaseID: r.ReleaseID, CommitSHA: r.CommitSHA,
			PreviousReleaseID: r.PreviousReleaseID, Services: r.Services, Actor: r.Actor,
			Status: r.Status, Detail: r.Detail, StartedAt: r.StartedAt.UTC(), FinishedAt: utcPtr(r.FinishedAt),
		})
	}
	if *jsonOut || jsonEnabled() {
		jsonEmit(osStdout, rows)
		return 0
	}
	reportReleaseHistory(osStdout, rows)
	return 0
}

func reportReleaseHistory(w io.Writer, rows []releaseHistoryRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "(no release activations recorded)")
		return
	}
	_, _ = fmt.Fprintf(w, "%-20s  %-24s  %-8s  %-11s  %-12s  %-12s  %-9s  %s\n", "STARTED", "HOST", "KIND", "STATUS", "RELEASE", "PREVIOUS", "DURATION", "ACTOR")
	for _, r := range rows {
		duration := "-"
		if r.FinishedAt != nil {
			duration = r.FinishedAt.Sub(r.StartedAt).Round(time.Second).String()
		}
		previous := r.PreviousReleaseID
		if previous == "" {
			previous = "-"
		}
		_, _ = fmt.Fprintf(w, "%-20s  %-24s  %-8s  %-11s  %-12s  %-12s  %-9s  %s\n",
			r.StartedAt.Format(time.RFC3339), r.Host, r.Kind, r.Status, shortRelease(r.ReleaseID), shortRelease(previous), duration, r.Actor)
		if len(r.Services) > 0 {
			_, _ = fmt.Fprintf(w, "    services: %s\n", strings.Join(r.Services, ","))
		}
		if r.Detail != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", r.Detail)
		}
	}
}

// shortRelease abbreviates a commit-named release id to 12 characters, as
// git does; other ids print whole.
func shortRelease(id string) string {
	if len(id) == 40 && strings.Trim(id, "0123456789abcdef") == "" {
		return id[:12]
	}
	return id
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
