//go:build !no_pg

// adr: 581
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/state"
)

type databaseSQLPinsWorkerFailureStore struct {
	*databaseWorkerFailureStore
	losePinsRecord   bool
	beforePinsRecord func()
}

func (s *databaseSQLPinsWorkerFailureStore) RecordProjectEnvironmentClonePostgresDatabaseSQLPins(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, parent, archive string, sealed copydatabases.SealedPreparation) (state.ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error) {
	if s.beforePinsRecord != nil {
		s.beforePinsRecord()
	}
	r, first, err := s.PgStore.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(ctx, l, id, oid, parent, archive, sealed)
	if err == nil && s.losePinsRecord {
		s.losePinsRecord = false
		return state.ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, managedpostgres.ErrUnavailable
	}
	return r, first, err
}

func cloneDatabaseSQLPinsWorkerFixture(t *testing.T, charge bool) (*databaseWorkerFixture, *databaseSQLPinsWorkerFailureStore) {
	t.Helper()
	f := cloneDatabaseWorkerFixture(t)
	s := &databaseSQLPinsWorkerFailureStore{databaseWorkerFailureStore: f.store}
	f.x.f.srv.store = s
	if charge {
		chargeDatabaseSQLPinsWorker(t, f, f.closedOID)
	}
	return f, s
}

func chargeDatabaseSQLPinsWorker(t *testing.T, f *databaseWorkerFixture, oid uint32) {
	t.Helper()
	x := f.x
	i, err := x.store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), x.f.lease, x.source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = x.store.ReserveProjectEnvironmentClonePostgresArchive(t.Context(), x.f.lease, state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: i.Sealed.Scope, DatabaseOID: oid, InventoryFingerprint: i.Sealed.Fingerprint, KeyID: mfaIdentities()[0].Recipient().String(), StorageID: "private-worker-artifacts", StorageFingerprint: strings.Repeat("d", 64), ReservedBytes: 8192}, state.ProjectEnvironmentClonePostgresArchiveLimits{Count: api.PostgresCopyArchivesPerAccountMax, Bytes: api.PostgresCopyArchiveBytesPerAccountMax})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPGClonePostgresDatabaseSQLPinsWorkerRetainsOriginalReceiptAcrossLostRecordAndHandoff(t *testing.T) {
	f, s := cloneDatabaseSQLPinsWorkerFixture(t, true)
	x := f.x
	s.losePinsRecord = true
	r, _, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !r.CreatedAt().IsZero() || !f.exists(t, f.closed) {
		t.Fatal("lost record supplied authority or lost owned database", err)
	}
	original, err := s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(t.Context(), x.f.lease, x.source.source.ID, f.closedOID)
	if err != nil {
		t.Fatal(err)
	}
	before := x.p.targetSQLCalls
	oldKey := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, oldKey} }
	setSecretRecipient = nil
	service := x.f.srv.managedPostgres
	x.f.srv.managedPostgres = nil
	r, owner, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
	if err != nil || !sameClonePostgresDatabaseSQLPins(owner, original) || x.p.targetSQLCalls != before {
		t.Fatal("original pins required SQL/current recipient", err)
	}
	pins, err := r.TargetForWorker()
	if err != nil || pins.DatabaseOID == f.closedOID || pins.OwnerID != x.target.OwnerID {
		t.Fatal("recovered pins changed independent mapping", err)
	}
	createdAt := r.CreatedAt()
	x.f.srv.managedPostgres = service
	old := x.f.lease
	if err = s.ReleaseProjectEnvironmentCloneLease(t.Context(), old, 0); err != nil {
		t.Fatal(err)
	}
	x.f.lease, err = s.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), old, x.source, f.exports, f.closedOID); !errors.Is(err, state.ErrConflict) || x.p.targetSQLCalls != before {
		t.Fatal("stale pins dispatched SQL", err)
	}
	r, err = x.f.srv.projectEnvironmentClonePostgresDatabasePreparationForSQL(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
	if err != nil || !r.CreatedAt().Equal(createdAt) {
		t.Fatal("handoff replaced original preparation", err)
	}
	var closed bool
	if err = x.targetRoot.QueryRow(t.Context(), "SELECT NOT datallowconn AND NOT datistemplate FROM pg_database WHERE oid=$1::oid", pins.DatabaseOID).Scan(&closed); err != nil || !closed {
		t.Fatal("pin verification opened database", err)
	}
	x.assertPrivate(t)
	before = x.p.targetSQLCalls
	if _, _, err = x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.unprojected, f.closedOID); !errors.Is(err, managedpostgres.ErrConflict) || x.p.targetSQLCalls != before {
		t.Fatal("recovery discarded original admission", err)
	}
}

func TestPGClonePostgresDatabaseSQLPinsWorkerRejectsUnchargedAndChangedFirstCapturePrerequisites(t *testing.T) {
	for _, fault := range []string{"uncharged", "recipient", "provider pre", "provider post", "plan changed at record", "archive changed at record", "unplanned source"} {
		t.Run(fault, func(t *testing.T) {
			f, s := cloneDatabaseSQLPinsWorkerFixture(t, fault != "uncharged")
			x := f.x
			if fault != "uncharged" && fault != "unplanned source" {
				if _, _, _, err := x.f.srv.projectEnvironmentClonePostgresDatabasePlan(t.Context(), x.f.lease, x.source, f.exports); err != nil {
					t.Fatal(err)
				}
			}
			oid := f.closedOID
			switch fault {
			case "recipient":
				setSecretRecipient = func() *age.X25519Recipient { other, _ := age.GenerateX25519Identity(); return other.Recipient() }
			case "provider pre":
				x.beforeSQL = func(context.Context) error { return managedpostgres.ErrConflict }
			case "provider post":
				x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
			case "plan changed at record":
				s.beforePinsRecord = func() {
					if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_database_plans SET ciphertext_sha256=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID); err != nil {
						t.Fatal(err)
					}
				}
			case "archive changed at record":
				s.beforePinsRecord = func() {
					if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID); err != nil {
						t.Fatal(err)
					}
				}
			case "unplanned source":
				oid = 4294967295
			}
			before := x.p.targetSQLCalls
			r, _, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, oid)
			if err == nil || !r.CreatedAt().IsZero() {
				t.Fatal("failed first capture supplied pins")
			}
			var count int
			if err = x.f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_database_sql_pins WHERE operation_id=$1", x.f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed first capture retained child owner", err)
			}
			if fault == "uncharged" || fault == "recipient" || fault == "unplanned source" {
				if f.exists(t, f.closed) || x.p.targetSQLCalls != before {
					t.Fatal("invalid prerequisite dispatched CREATE")
				}
			}
			if fault == "provider post" {
				x.p.targetSQLAfterError = nil
				r, _, err = x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
				if err != nil || r.CreatedAt().IsZero() {
					t.Fatal("provider retry lost original owned creation", err)
				}
			}
			x.assertPrivate(t)
		})
	}
}

func TestPGClonePostgresDatabaseSQLPinsWorkerChecksOriginalTargetJournalAndLiveAuthorityBeforeUse(t *testing.T) {
	for _, fault := range []string{"not prepared", "journal missing", "time changed", "database missing", "stale", "phase", "archive changed", "provider post"} {
		t.Run(fault, func(t *testing.T) {
			f, s := cloneDatabaseSQLPinsWorkerFixture(t, true)
			x := f.x
			if fault == "not prepared" {
				r, err := x.f.srv.projectEnvironmentClonePostgresDatabasePreparationForSQL(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
				if !errors.Is(err, state.ErrNotFound) || !r.CreatedAt().IsZero() || x.p.targetSQLCalls != 0 || f.exists(t, f.closed) || x.roleExists(t) {
					t.Fatal("verification prepared an absent parent/database", err)
				}
				x.assertPrivate(t)
				return
			}
			r, original, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
			if err != nil {
				t.Fatal(err)
			}
			createdAt := r.CreatedAt()
			cfg := x.targetRoot.Config().Copy()
			cfg.Database = x.target.DatabaseName
			c, err := pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close(context.Background())
			switch fault {
			case "journal missing":
				_, err = c.Exec(t.Context(), "DROP SCHEMA gregale_copy_databases CASCADE")
			case "time changed":
				_, err = c.Exec(t.Context(), "UPDATE gregale_copy_databases.databases SET created_at=created_at+interval '1 second' WHERE source_oid=$1::oid", f.closedOID)
			case "database missing":
				_, err = x.targetRoot.Exec(t.Context(), "DROP DATABASE "+pgx.Identifier{f.closed}.Sanitize())
			case "stale":
				err = s.ReleaseProjectEnvironmentCloneLease(t.Context(), x.f.lease, 0)
			case "phase":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", x.f.lease.Operation.ID)
			case "archive changed":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=$1 WHERE operation_id=$2", strings.Repeat("f", 64), x.f.lease.Operation.ID)
			case "provider post":
				x.p.targetSQLAfterError = managedpostgres.ErrUnavailable
			}
			if err != nil {
				t.Fatal(err)
			}
			before := x.p.targetSQLCalls
			r, err = x.f.srv.projectEnvironmentClonePostgresDatabasePreparationForSQL(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
			if err == nil || !r.CreatedAt().IsZero() {
				t.Fatal("unqualified pins supplied SQL authority")
			}
			if fault == "stale" || fault == "phase" || fault == "archive changed" {
				if x.p.targetSQLCalls != before {
					t.Fatal("lost durable authority dispatched SQL")
				}
			}
			if fault == "journal missing" {
				var exists bool
				if err = c.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_copy_databases')").Scan(&exists); err != nil || exists {
					t.Fatal("verification repaired missing journal", err)
				}
			}
			if fault == "database missing" && f.exists(t, f.closed) {
				t.Fatal("verification recreated missing database")
			}
			if fault == "provider post" {
				x.p.targetSQLAfterError = nil
				r, err = x.f.srv.projectEnvironmentClonePostgresDatabasePreparationForSQL(t.Context(), x.f.lease, x.source, f.exports, f.closedOID)
				if err != nil || !r.CreatedAt().Equal(createdAt) {
					t.Fatal("postcheck retry rebased preparation", err)
				}
				owner, e := s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(t.Context(), x.f.lease, x.source.source.ID, f.closedOID)
				if e != nil || !sameClonePostgresDatabaseSQLPins(owner, original) {
					t.Fatal("read-only retry replaced pins", e)
				}
			}
			x.assertPrivate(t)
		})
	}
}

func TestPGClonePostgresDatabaseSQLPinsWorkerRetainsEveryCapturedDatabaseIncludingClosedAndTemplates(t *testing.T) {
	f, _ := cloneDatabaseSQLPinsWorkerFixture(t, false)
	x := f.x
	requirements, err := f.exports.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]bool{}
	for _, d := range requirements {
		chargeDatabaseSQLPinsWorker(t, f, d.Database.OID)
		r, _, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.exports, d.Database.OID)
		if err != nil {
			t.Fatal(err)
		}
		target, err := r.TargetForWorker()
		if err != nil || seen[target.DatabaseOID] || target.DatabaseName != d.Database.Name || target.OwnerID != x.target.OwnerID {
			t.Fatal("omitted or rebound database disposition", err)
		}
		seen[target.DatabaseOID] = true
	}
	var count int
	if err = x.f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_postgres_database_sql_pins WHERE operation_id=$1", x.f.lease.Operation.ID).Scan(&count); err != nil || count != len(requirements) {
		t.Fatal("incomplete durable child pin set", err)
	}
	for _, d := range requirements {
		if _, err = x.f.srv.projectEnvironmentClonePostgresDatabasePreparationForSQL(t.Context(), x.f.lease, x.source, f.exports, d.Database.OID); err != nil {
			t.Fatal("complete-set recovery", err)
		}
	}
	x.assertPrivate(t)
}
