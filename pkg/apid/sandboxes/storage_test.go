package sandboxes_test

// adr: 20261122132055 §8 — GET /v1/sandbox-storage shows an account's
// metered storage hours of a window (the current month by default), their
// GB-hours and the plan's free allowance; it is not billable yet.

import (
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxStorage(t *testing.T) {
	f := newFixture(t)
	var p api.Problem
	if code := f.call(t, f.h.Storage, pro(), http.MethodGet, "/v1/sandbox-storage", "", "", &p); code != http.StatusUnprocessableEntity {
		t.Fatalf("storage while persistent sessions are off = %d %+v", code, p)
	}
	f.h.Persistent = true
	month := time.Date(f.now.Year(), f.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	f.store.storage = map[string][]agentstore.SandboxStorageHour{account: {
		{Hour: month.Add(-time.Hour), PausedSessions: 9, CaptureBytes: 1 << 40},
		{Hour: month, PausedSessions: 1, CaptureBytes: 1 << 30, VolumeBytes: 1 << 30},
		{Hour: month.Add(time.Hour), PausedSessions: 1, CaptureBytes: 1 << 29},
	}}
	if !f.now.After(month.Add(time.Hour)) {
		t.Skip("the fixture's clock is within the month's first two hours")
	}
	var got api.SandboxStorageUsage
	if code := f.call(t, f.h.Storage, pro(), http.MethodGet, "/v1/sandbox-storage", "", "", &got); code != http.StatusOK {
		t.Fatalf("storage = %d", code)
	}
	if len(got.Hours) != 2 || math.Abs(got.GBHours-2.5) > 1e-9 || got.Billable ||
		got.FreeGB != api.SandboxPausedBytesFreePerAccount[api.PlanPro.PlanIndex()]>>30 || !got.From.Equal(month) {
		t.Fatalf("storage = %+v", got)
	}
	for name, target := range map[string]string{
		"a bad time":        "/v1/sandbox-storage?from=yesterday",
		"an empty window":   "/v1/sandbox-storage?from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z",
		"too long a window": "/v1/sandbox-storage?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z",
	} {
		if code := f.call(t, f.h.Storage, pro(), http.MethodGet, target, "", "", &p); code != http.StatusBadRequest {
			t.Errorf("%s = %d %+v", name, code, p)
		}
	}
}
