//go:build !no_pg

// adr:531
package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

type targetSQLPinsWorkerFailureStore struct {
	*state.PgStore
	loseRecord bool
}

func (s *targetSQLPinsWorkerFailureStore) RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, fp string, sealed copyarchive.SealedTarget) (state.ProjectEnvironmentClonePostgresTargetSQLPins, bool, error) {
	r, first, err := s.PgStore.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fp, sealed)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return r, false, managedpostgres.ErrUnavailable
	}
	return r, first, err
}

func (p *cloneSnapshotProvider) InspectSnapshotCopyTargetSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
	p.targetSQLInspections++
	if p.inspectTargetSQL == nil {
		return managedpostgres.SnapshotCopyTargetSQLObservation{}, managedpostgres.ErrUnavailable
	}
	return p.inspectTargetSQL(ctx, d, r)
}

func targetSQLPinsWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *targetSQLPinsWorkerFailureStore, *cloneSnapshotProvider, capturedProjectEnvironmentDatabasePlan, copyarchive.RestoreTarget) {
	t.Helper()
	x := cloneRoleWorkerFixture(t)
	f, p, source, target := x.f, x.p, x.source, x.target
	// This fixture exercises first bootstrap inspection, before any child/role
	// plan exists. It owns this synthetic initial receipt and removes it here.
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_postgres_target_sql_pins WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	store := &targetSQLPinsWorkerFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	p.inspectTargetSQL = func(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
		var zero managedpostgres.SnapshotCopyTargetSQLObservation
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(f.lease.ExpiresAt) || r.ResourceID != target.OwnerID || r.ExpectedProviderResourceID != target.ProviderResourceID || !r.ExpectedCreatedAt.Equal(target.ProviderCreatedAt) {
			return zero, managedpostgres.ErrConflict
		}
		actual, err := p.FindSnapshotCopyTarget(ctx, d, r)
		if err != nil {
			return zero, err
		}
		if !actual.Prepared {
			return zero, managedpostgres.ErrUnavailable
		}
		conn, err := p.targetSQLConnect(ctx, target.DatabaseName)
		if err != nil {
			return zero, err
		}
		defer func() { _ = conn.Close(context.Background()) }()
		var i managedpostgres.SnapshotCopyTargetSQLIdentity
		if err := conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int/10000,current_database(),current_user,d.oid,r.oid
 FROM pg_catalog.pg_database d,pg_catalog.pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user`).Scan(&i.PostgresMajor, &i.DatabaseName, &i.RoleName, &i.DatabaseOID, &i.RoleOID); err != nil {
			return zero, err
		}
		// Actual SQL identity is read from a task-owned local database. Physical
		// provider/capture metadata is synthetic; fixed bootstrap placement and
		// ordinary-owner privileges are qualified separately by Neon contracts.
		return managedpostgres.SnapshotCopyTargetSQLObservation{ProviderResourceID: target.ProviderResourceID, ProviderCreatedAt: target.ProviderCreatedAt, DataResourceID: target.DataResourceID, EndpointID: target.EndpointID, EndpointCreatedAt: target.EndpointCreatedAt, Identity: i}, nil
	}
	return f, store, p, source, target
}

func TestPGClonePostgresTargetSQLPinsWorkerRecoversCommittedReplyAndOriginalKeyWithoutRediscovery(t *testing.T) {
	f, store, p, source, target := targetSQLPinsWorkerFixture(t)
	previous := mfaIdentities()[0]
	store.loseRecord = true
	if _, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source); !errors.Is(err, managedpostgres.ErrUnavailable) || p.targetSQLInspections != 1 {
		t.Fatalf("lost committed target SQL pins reply: %v", err)
	}
	original, err := store.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(t.Context(), f.lease, source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := age.GenerateX25519Identity()
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, previous} }
	f.srv.managedPostgres = nil
	setSecretRecipient = nil
	got, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source)
	if err != nil || got != target || p.targetSQLInspections != 1 {
		t.Fatalf("handoff rediscovered target SQL pins: %v", err)
	}
	r, err := store.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(t.Context(), f.lease, source.source.ID)
	if err != nil || !r.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) || r.Sealed.KeyID != previous.Recipient().String() {
		t.Fatalf("rotation replaced committed target SQL ciphertext: %v", err)
	}
	var private bool
	if err := f.pool.QueryRow(t.Context(), "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("SQL pins published dataset readiness: %v", err)
	}
}

func TestPGClonePostgresTargetSQLPinsWorkerNeverReplacesUnreadableCommittedPins(t *testing.T) {
	f, store, p, source, _ := targetSQLPinsWorkerFixture(t)
	if _, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source); err != nil {
		t.Fatal(err)
	}
	previous := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current} }
	if _, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source); !errors.Is(err, managedpostgres.ErrUnavailable) || p.targetSQLInspections != 1 {
		t.Fatalf("missing key rediscovered target SQL pins: %v", err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{previous} }
	if _, err := f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_target_sql_pins SET ciphertext=decode('00','hex') WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source); !errors.Is(err, state.ErrConflict) || p.targetSQLInspections != 1 {
		t.Fatalf("damaged pins rediscovered target SQL: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_target_sql_pins WHERE operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unreadable target SQL pins lost ownership: %v", err)
	}
	if _, err := store.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(t.Context(), f.lease, source.source.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unreadable target SQL pins accepted")
	}
}

func TestPGClonePostgresTargetSQLPinsWorkerRejectsMissingKeysSourceAndInFlightOwnerDrift(t *testing.T) {
	for _, fault := range []string{"recipient", "key", "source", "stale", "provider", "observation", "owner_drift"} {
		t.Run(fault, func(t *testing.T) {
			f, store, p, source, target := targetSQLPinsWorkerFixture(t)
			calls := 0
			switch fault {
			case "recipient":
				setSecretRecipient = nil
			case "key":
				other, _ := age.GenerateX25519Identity()
				setSecretRecipient = func() *age.X25519Recipient { return other.Recipient() }
			case "source":
				source.hash = strings.Repeat("f", 64)
			case "stale":
				if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute); err != nil {
					t.Fatal(err)
				}
			case "provider":
				p.inspectTargetSQL = func(context.Context, managedpostgres.RestoreSourceDefinition, managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
					return managedpostgres.SnapshotCopyTargetSQLObservation{}, managedpostgres.ErrUnavailable
				}
				calls = 1
			case "observation":
				original := p.inspectTargetSQL
				p.inspectTargetSQL = func(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
					actual, err := original(ctx, d, r)
					actual.EndpointID = target.Scope.SourceDataResourceID
					return actual, err
				}
				calls = 1
			case "owner_drift":
				original := p.inspectTargetSQL
				p.inspectTargetSQL = func(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
					actual, err := original(ctx, d, r)
					if err == nil {
						_, err = f.pool.Exec(ctx, "UPDATE managed_postgres_databases SET observed_generation=1 WHERE id=$1", target.OwnerID)
					}
					return actual, err
				}
				calls = 1
			}
			got, err := f.srv.projectEnvironmentClonePostgresTargetSQLPins(t.Context(), f.lease, source)
			if err == nil || got != (copyarchive.RestoreTarget{}) || p.targetSQLInspections != calls {
				t.Fatalf("unqualified target SQL pins captured: %v", err)
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_target_sql_pins WHERE operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected inspection retained target SQL pins: %v", err)
			}
		})
	}
}
