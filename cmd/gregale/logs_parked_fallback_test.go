package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParkedLogsSelectionPicksNewestInstance(t *testing.T) {
	instances := []api.InstanceResponse{
		{ID: "old", StartedAt: "2026-10-13T23:50:00Z", LastRequestAt: "2026-10-13T23:55:00Z"},
		{ID: "new", StartedAt: "2026-10-14T17:04:34Z", LastRequestAt: "2026-10-14T17:04:40Z"},
		{ID: "unknown-start"},
	}
	ins, sel, ok := main.ParkedLogsSelection(instances, time.Time{})
	if !ok || ins.ID != "new" || sel.InstanceID != "new" || sel.Date != "2026-10-14" {
		t.Fatalf("selection = %+v %+v %v, want the newest instance on 2026-10-14", ins, sel, ok)
	}
	if _, _, ok := main.ParkedLogsSelection(instances, time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)); ok {
		t.Fatal("an instance idle since before --since was selected")
	}
	if _, _, ok := main.ParkedLogsSelection(nil, time.Time{}); ok {
		t.Fatal("selected an instance from an empty list")
	}
}

// A parked app's logs show the newest instance's retained lines, filtered by
// --grep, instead of only naming the archive flags (H10-72).
func TestCmdLogsParkedAppShowsRetainedLogs(t *testing.T) {
	main.ResetJSONOut(t)
	var archiveQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/instances"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `[{"id":"inst-new","state":"sleeping","started_at":"2026-10-14T17:04:34Z"},{"id":"inst-old","state":"stopped","started_at":"2026-10-13T09:00:00Z"}]`)
		case r.URL.Query().Get("archive_instance") != "" || r.URL.Query().Get("instance") != "" || strings.Contains(r.URL.Path, "archive"):
			archiveQuery = r.URL.String()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: ready on :8080\n\ndata: ERROR boom\n\nevent: end\ndata: {\"reason\":\"archive_complete\"}\n\n")
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: degraded\ndata: {\"code\":\"not_found\"}\n\n")
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	stdout, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.CmdLogs([]string{"myapp", "--grep", "boom"})
	restoreErr()
	restoreOut()
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(archiveQuery, "inst-new") || !strings.Contains(archiveQuery, "2026-10-14") {
		t.Fatalf("archive request = %q, want the newest instance and its UTC day", archiveQuery)
	}
	if !strings.Contains(stderr.String(), "myapp is parked; showing the retained logs of its most recent instance inst-new") {
		t.Fatalf("stderr = %q, want the parked header", stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "boom") || strings.Contains(out, "ready on") {
		t.Fatalf("stdout = %q, want only the --grep match", out)
	}
}

func TestNormalizeLogsSinceRejectsFutureRuntimeTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC)
	if _, err := main.NormalizeLogsSince("2030-01-01T00:00:00Z", "runtime", now); err == nil || !strings.Contains(err.Error(), "in the past") {
		t.Fatalf("future runtime --since err = %v, want in-the-past error", err)
	}
	if got, err := main.NormalizeLogsSince("2026-10-14T18:00:00Z", "runtime", now); err != nil || got == "" {
		t.Fatalf("past runtime --since = %q, %v", got, err)
	}
}

// TestParkedArchivePendingNote pins H10-122: for a few minutes after a park
// the archive does not exist yet, and the reply read like data loss.
func TestParkedArchivePendingNote(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		ins  api.InstanceResponse
		want bool
	}{
		"parked two minutes ago":  {ins: api.InstanceResponse{ParkedAt: now.Add(-2 * time.Minute).Format(time.RFC3339)}, want: true},
		"parked an hour ago":      {ins: api.InstanceResponse{ParkedAt: now.Add(-time.Hour).Format(time.RFC3339)}, want: false},
		"no park, recent request": {ins: api.InstanceResponse{LastRequestAt: now.Add(-3 * time.Minute).Format(time.RFC3339)}, want: true},
		"no timestamps":           {ins: api.InstanceResponse{}, want: false},
		"park time in the future": {ins: api.InstanceResponse{ParkedAt: now.Add(time.Minute).Format(time.RFC3339)}, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			note := main.ParkedArchivePendingNote(tc.ins, now)
			if got := note != ""; got != tc.want {
				t.Fatalf("note = %q, want present=%v", note, tc.want)
			}
			if tc.want && !strings.Contains(note, "retry shortly") {
				t.Fatalf("note does not say to retry: %q", note)
			}
		})
	}
}
