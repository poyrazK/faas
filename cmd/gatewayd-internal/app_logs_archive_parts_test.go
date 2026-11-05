// adr: 20261028110000 — the archive read streams a day's whole-day object,
// then its part objects.
package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

// A day archived as a whole-day object and then as part objects reads back
// as one day, whole-day lines first, ending complete.
func TestArchiveStream_ReadsWholeDayThenParts(t *testing.T) {
	day := archiveTestDate()
	stamp := day + "T12:00:00Z"
	whole := "/test-bucket/" + logarchive.DayKey("inst-abc", day)
	part := logarchive.DayPartPrefix("inst-abc", day) + "0000000000000000001-aa.jsonl.gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Has("prefix"):
			_, _ = io.WriteString(w, "<ListBucketResult><Contents><Key>"+part+"</Key></Contents></ListBucketResult>")
		case r.URL.Path == whole:
			_, _ = w.Write(gzipJSONL(t, []string{`{"seq":1,"stream":"stdout","ts":"` + stamp + `","msg":"before the switch"}`}))
		case r.URL.Path == "/test-bucket/"+part:
			_, _ = w.Write(gzipJSONL(t, []string{`{"seq":2,"stream":"stdout","ts":"` + stamp + `","msg":"first part"}`}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	h := &ArchiveLogsHandler{
		Scope: ownedArchiveScope(), S3: newArchiveTestClient(t, srv), Bucket: "test-bucket",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Backstop: 5 * time.Second,
	}
	out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "archive=1&instance=inst-abc&date="+day)
	before, first := strings.Index(out, "before the switch"), strings.Index(out, "first part")
	if before < 0 || first < before {
		t.Fatalf("want the whole-day line, then the part's line:\n%s", out)
	}
	if !strings.Contains(out, `"reason":"archive_complete"`) {
		t.Fatalf("day did not end complete:\n%s", out)
	}
}
