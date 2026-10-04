//go:build !no_pg

// adr: 581
package state_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkpointSelectionSeal(t *testing.T, scope checkpointselection.Scope) (*age.X25519Identity, checkpointselection.Sealed) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	key[0] = 1
	r := managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{OwnerToken: scope.OperationID, SourceResourceID: scope.SourceDataResourceID}, DatabaseNames: []string{"source_private_beta", "source_private_alpha"}}
	sealed, err := checkpointselection.Seal(identity.Recipient(), scope, r, key)
	if err != nil {
		t.Fatal(err)
	}
	return identity, sealed
}

func TestPgClonePostgresCheckpointSelectionRetainsOriginalAcrossHandoff(t *testing.T) {
	s, ctx, pool, l, id, m := clonePostgresMaintenanceFixture(t)
	m = finishCloneMaintenance(t, s, ctx, l, id, m)
	if len(l.Operation.Resources) != 0 {
		t.Fatal("selection fixture already has a data point")
	}
	scope, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id)
	if err != nil || scope.OperationID != l.Operation.ID || scope.MaintenanceID != m.ID || scope.MaintenanceOwnerOID != m.OwnerOID || scope.MaintenanceDatabaseOID != m.DatabaseOID {
		t.Fatalf("original scope: %+v %v", scope, err)
	}
	identity, sealed := checkpointSelectionSeal(t, scope)
	r, created, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed)
	if err != nil || !created || r.RetainedAt.IsZero() || r.Sealed.Fingerprint != sealed.Fingerprint {
		t.Fatalf("retention: %+v %v", r, err)
	}
	stale := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if actual, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, stale, id, sealed); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
		t.Fatalf("stale record: %+v %v", actual, err)
	}
	actual, created, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed)
	if err != nil || created || !actual.RetainedAt.Equal(r.RetainedAt) || !bytes.Equal(actual.Sealed.Ciphertext, r.Sealed.Ciphertext) {
		t.Fatalf("replayed original: %+v %v", actual, err)
	}
	_, replacement := checkpointSelectionSeal(t, scope)
	if actual, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, replacement); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
		t.Fatalf("replacement selection: %+v %v", actual, err)
	}
	actual, err = s.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, l, id)
	if err != nil || !bytes.Equal(actual.Sealed.Ciphertext, sealed.Ciphertext) {
		t.Fatalf("original was rewritten: %+v %v", actual, err)
	}
	selection, err := checkpointselection.Open([]*age.X25519Identity{identity}, scope, actual.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	request, err := selection.RequestForWorker(scope)
	if err != nil || request.OwnerToken != l.Operation.ID || len(request.DatabaseNames) != 2 {
		t.Fatalf("retained request: %+v %v", request, err)
	}
	var plaintext bool
	if err := pool.QueryRow(ctx, "select scope::text like '%source_private%' or encode(ciphertext,'escape') like '%source_private%' from project_environment_clone_postgres_checkpoint_selections where operation_id=$1", l.Operation.ID).Scan(&plaintext); err != nil || plaintext {
		t.Fatalf("plaintext selection persisted: %v", err)
	}
	if _, err := pool.Exec(ctx, "delete from project_environment_clone_postgres_write_fences where operation_id=$1", l.Operation.ID); err == nil {
		t.Fatal("selection lost source recovery parent")
	}
	if _, err := pool.Exec(ctx, "delete from managed_postgres_checkpoint_maintenance where id=$1", m.ID); err == nil {
		t.Fatal("selection lost maintenance owner")
	}
	l = compensatePostgresWriteFence(t, s, ctx, l)
	if _, err := s.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, l, id); err != nil || !bytes.Equal(actual.Sealed.Ciphertext, sealed.Ciphertext) {
		t.Fatalf("abandonment lost original selection: %+v %v", actual, err)
	}
	if actual, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
		t.Fatalf("compensation rewrote intent: %+v %v", actual, err)
	}
}

func TestPgClonePostgresCheckpointSelectionRequiresHoldReadyOwnerAndPlacement(t *testing.T) {
	s, ctx, pool, l, id := clonePostgresWriteFenceFixture(t)
	if actual, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id); err == nil || actual != (checkpointselection.Scope{}) {
		t.Fatalf("missing hold: %+v %v", actual, err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, l, id); err != nil {
		t.Fatal(err)
	}
	m, err := s.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, l, id)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) || actual != (checkpointselection.Scope{}) {
		t.Fatalf("unready owner: %+v %v", actual, err)
	}
	m = finishCloneMaintenance(t, s, ctx, l, id, m)
	scope, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id)
	if err != nil {
		t.Fatal(err)
	}
	_, sealed := checkpointSelectionSeal(t, scope)
	wrong := sealed
	wrong.Scope.SourceDataResourceID += "-other"
	if actual, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, wrong); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
		t.Fatalf("foreign source: %+v %v", actual, err)
	}
	if _, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_checkpoint_maintenance set owner_oid=owner_oid+1 where id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
		t.Fatalf("changed SQL owner: %+v %v", actual, err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_checkpoint_maintenance set owner_oid=$2 where id=$1", m.ID, m.OwnerOID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set postgres_major=$2 where id=$1", id, scope.PostgresMajor+1); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) || actual != (checkpointselection.Scope{}) {
		t.Fatalf("changed live major: %+v %v", actual, err)
	}
}

func TestPgClonePostgresCheckpointSelectionLeaseExpiryAfterSourceLock(t *testing.T) {
	for _, action := range []string{"scope", "read", "record"} {
		t.Run(action, func(t *testing.T) {
			s, ctx, pool, l, id, m := clonePostgresMaintenanceFixture(t)
			finishCloneMaintenance(t, s, ctx, l, id, m)
			scope, err := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id)
			if err != nil {
				t.Fatal(err)
			}
			_, sealed := checkpointSelectionSeal(t, scope)
			if action == "read" {
				if _, _, err := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := blocker.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", id); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				var err error
				switch action {
				case "scope":
					r, e := s.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, id)
					err = e
					if r != (checkpointselection.Scope{}) {
						err = errors.New("expired scope returned metadata")
					}
				case "read":
					r, e := s.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, l, id)
					err = e
					if !reflect.DeepEqual(r, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
						err = errors.New("expired read returned metadata")
					}
				case "record":
					r, created, e := s.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed)
					err = e
					if created || !reflect.DeepEqual(r, state.ProjectEnvironmentClonePostgresCheckpointSelection{}) {
						err = errors.New("expired record returned metadata")
					}
				}
				result <- err
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ReadProjectEnvironmentCloneDatabaseSource%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("selection did not reach confirmed source lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(max(0, time.Until(l.ExpiresAt)) + 30*time.Millisecond)
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s: %v", action, err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_checkpoint_selections where operation_id=$1", l.Operation.ID).Scan(&count); err != nil || count != map[bool]int{true: 1, false: 0}[action == "read"] {
				t.Fatalf("expired %s mutated intent: %d %v", action, count, err)
			}
		})
	}
}
