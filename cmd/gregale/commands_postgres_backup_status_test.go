package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderPostgresBackupStatus(t *testing.T) {
	backupAt := time.Date(2026, 11, 21, 15, 0, 0, 0, time.FixedZone("UTC+3", 10800))
	status := api.ManagedPostgresBackupStatus{RestoreWindowSeconds: 604800, LastBaseBackupAt: &backupAt}
	var out bytes.Buffer
	renderPostgresBackupStatus(&out, status)
	for _, want := range []string{"Restore window: 7d", "Last recorded base backup: 2026-11-21T12:00:00Z", "Restore verification: unverified"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q in %s", want, &out)
		}
	}

	out.Reset()
	status.LatestRestoreDrill = &api.ManagedPostgresRestoreDrillStatus{
		Outcome: "failed", StartedAt: backupAt.Add(time.Hour), TargetAt: backupAt,
	}
	status.LastVerifiedRestore = &api.ManagedPostgresRestoreDrillStatus{
		Outcome: "succeeded", TargetAt: backupAt.Add(-time.Minute), RecoveredTo: &backupAt,
	}
	renderPostgresBackupStatus(&out, status)
	for _, want := range []string{"Latest restore drill: failed", "Last verified restore: target 2026-11-21T11:59:00Z",
		"replayed to 2026-11-21T12:00:00Z"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q in %s", want, &out)
		}
	}
}
