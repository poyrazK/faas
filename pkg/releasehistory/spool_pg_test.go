//go:build !no_pg

package releasehistory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasehistory"
)

// A compute host's spool is imported under the host CD names, once: importing
// it again adds nothing, and a line that is not a finished activation is
// refused.
func TestImportSpooledActivations(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	started := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	lines := []releasehistory.Line{
		{ID: "6f1c2a52-6a7b-4c8e-9a0d-1b2c3d4e5f60", Host: "spoofed", Kind: releasehistory.KindDeploy, ReleaseID: "overlay-1",
			CommitSHA: strings.Repeat("a", 40), PreviousReleaseID: "base", Services: []string{"gatewayd-internal"},
			Actor: "cd-edge-units run 9", Status: releasehistory.StatusSucceeded, StartedAt: started, FinishedAt: started.Add(time.Minute)},
		{ID: "6f1c2a52-6a7b-4c8e-9a0d-1b2c3d4e5f61", Kind: releasehistory.KindRollback, ReleaseID: "base", PreviousReleaseID: "overlay-1",
			Services: []string{"gatewayd-internal"}, Actor: "cd-edge-units run 9", Status: releasehistory.StatusSucceeded,
			StartedAt: started.Add(2 * time.Minute), FinishedAt: started.Add(3 * time.Minute)},
	}
	for round := 0; round < 2; round++ {
		for i, l := range lines {
			l.Host = "fsn-3.faas"
			isNew, err := store.Import(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			if isNew != (round == 0) {
				t.Fatalf("round %d line %d: new = %t", round, i, isNew)
			}
		}
	}
	rows, err := store.List(ctx, "fsn-3.faas", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != lines[1].ID || rows[0].Kind != releasehistory.KindRollback || rows[1].ReleaseID != "overlay-1" ||
		rows[1].Actor != "cd-edge-units run 9" || len(rows[1].Services) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if spoofed, err := store.List(ctx, "spoofed", 10); err != nil || len(spoofed) != 0 {
		t.Fatalf("a spool named its own host: %+v, %v", spoofed, err)
	}

	bad := lines[0]
	bad.ID, bad.Host, bad.Status = "6f1c2a52-6a7b-4c8e-9a0d-1b2c3d4e5f62", "fsn-3.faas", releasehistory.StatusInProgress
	if _, err := store.Import(ctx, bad); err == nil {
		t.Fatal("Import accepted an unfinished activation")
	}
}
