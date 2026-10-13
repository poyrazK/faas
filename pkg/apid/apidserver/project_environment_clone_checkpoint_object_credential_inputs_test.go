//go:build !no_pg

// adr: 590
package apidserver

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGCloneCheckpointObjectCredentialInputs(t *testing.T) {
	f, s, d := checkpointObjectVerificationFixture(t)
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), f.lease, retention); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unverified bucket admitted", err)
	}
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err = f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), f.lease, retention)
	if err != nil || len(original.Buckets) != 2 {
		t.Fatal("verified graph", err)
	}
	views, err := s.PgStore.ProjectEnvironmentCloneBindings(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range views {
		for _, b := range v.Buckets {
			count++
			found := false
			for _, edge := range original.Buckets {
				if edge.AppID == v.AppID && edge.Source.ID == b.ID {
					found = true
					if !reflect.DeepEqual(edge.Source, b) || len(edge.Credentials) != len(b.Credentials) {
						t.Fatal("frozen definitions lost")
					}
				}
			}
			if !found {
				t.Fatal("bucket missing")
			}
		}
	}
	if count != len(original.Buckets) {
		t.Fatal("extra bucket")
	}
	old := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if _, err = s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), old, retention); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker accepted", err)
	}
	f.lease, _, err = f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err = f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), f.lease, retention)
	if err != nil || !reflect.DeepEqual(original, recovered) {
		t.Fatal("handoff changed roster", err)
	}
	expired := retention
	expired.RetainedUntil = time.Now()
	if _, err = s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), f.lease, expired); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired retention accepted", err)
	}
	target := original.Buckets[0].Target.TargetBucketID
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialInputs(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) {
		t.Fatal("public target accepted", err)
	}
}
