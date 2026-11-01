package main

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The archive read's backstop is an idle timeout. It used to be a fixed
// total, so a day that took longer than the backstop to stream ended
// archive_degraded halfway through even though lines kept arriving.
func TestArchiveStream_BackstopIsIdleNotTotal(t *testing.T) {
	srv := httptest.NewServer(archiveBucket(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		for i := 1; i <= 6; i++ {
			_, _ = gz.Write([]byte(`{"seq":` + strconv.Itoa(i) + `,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"line ` + strconv.Itoa(i) + `"}` + "\n"))
			_ = gz.Flush()
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
		_ = gz.Close()
	}))
	t.Cleanup(srv.Close)
	h := &ArchiveLogsHandler{
		Scope: ownedArchiveScope(), S3: newArchiveTestClient(t, srv), Bucket: "test-bucket",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Backstop: 200 * time.Millisecond, Heartbeat: time.Second,
	}
	out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "archive=1&instance=inst-abc&date="+archiveTestDate())
	if !strings.Contains(out, "line 6") || !strings.Contains(out, `"reason":"archive_complete"`) {
		t.Fatalf("a day that kept streaming was cut off:\n%s", out)
	}
}
