// adr: 590
package neon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSnapshotRestoreHydratesAsyncIdentityWithoutRepeatingCreate(t *testing.T) {
	for _, mode := range []string{"created", "identity_only", "not_visible_yet", "lost_reply", "recovered", "known_target", "identity", "snapshot", "project", "default", "finalized", "never_settles"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotRestoreFixture(t)
			f.ackIncomplete, f.listIncomplete = true, true
			f.incompleteReads = 1
			f.observationFault = mode
			if mode == "identity_only" {
				f.ackFault = mode
			}
			f.losePost = mode == "lost_reply"
			wantPosts := 1
			if mode == "recovered" || mode == "known_target" {
				f.rows = []branch{f.target()}
				wantPosts = 0
			}
			if mode == "known_target" {
				f.request.ExpectedTargetResourceID = "project-source/br-target"
			}
			timeout := 5 * time.Second
			if mode == "never_settles" {
				f.incompleteReads = 10000
				timeout = 250 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			actual, err := f.p.RestoreSnapshot(ctx, f.definition, f.request)
			f.mu.Lock()
			defer f.mu.Unlock()
			wantErr := error(nil)
			switch mode {
			case "identity", "snapshot", "project", "default", "finalized":
				wantErr = managedpostgres.ErrConflict
			case "never_settles":
				wantErr = managedpostgres.ErrUnavailable
			}
			if !errors.Is(err, wantErr) || (err == nil && (!actual.Restored || actual.ProviderResourceID != "project-source/br-target" || actual.ProviderSnapshotID != f.request.ProviderSnapshotID)) ||
				(err != nil && actual.ProviderResourceID != "") {
				t.Fatalf("restore %s: %+v, %v; want %v", mode, actual, err, wantErr)
			}
			if f.posts != wantPosts || f.exactReads < 2 {
				t.Fatalf("create/read counts: posts=%d reads=%d", f.posts, f.exactReads)
			}
		})
	}
}

func TestSnapshotRestoreRejectsContradictoryAcknowledgementBeforeReading(t *testing.T) {
	for _, fault := range []string{"missing_identity", "project", "snapshot", "source", "name", "default", "finalized"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotRestoreFixture(t)
			f.ackIncomplete, f.ackFault = true, fault
			actual, err := f.p.RestoreSnapshot(t.Context(), f.definition, f.request)
			want := managedpostgres.ErrConflict
			if fault == "missing_identity" {
				want = managedpostgres.ErrUnavailable
			}
			if !errors.Is(err, want) || actual.ProviderResourceID != "" || f.posts != 1 || f.exactReads != 0 {
				t.Fatalf("contradictory %s acknowledgement: %+v %v; posts=%d reads=%d", fault, actual, err, f.posts, f.exactReads)
			}
		})
	}
}
