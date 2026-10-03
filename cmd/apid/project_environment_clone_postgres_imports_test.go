//go:build !no_pg

// adr:375
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

type importWorkerFailureStore struct {
	*state.PgStore
	loseClaim, loseRecord bool
}

func (s *importWorkerFailureStore) ClaimProjectEnvironmentClonePostgresImport(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32) (state.ProjectEnvironmentClonePostgresImport, bool, error) {
	i, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresImport(ctx, l, id, oid)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return i, false, managedpostgres.ErrUnavailable
	}
	return i, dispatch, err
}

func (s *importWorkerFailureStore) RecordProjectEnvironmentClonePostgresImportExecution(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, e copyarchive.RestoreExecution) (state.ProjectEnvironmentClonePostgresImport, error) {
	i, err := s.PgStore.RecordProjectEnvironmentClonePostgresImportExecution(ctx, l, id, oid, e)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return i, managedpostgres.ErrUnavailable
	}
	return i, err
}

func (p *cloneSnapshotProvider) WithSnapshotCopyTargetDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, run managedpostgres.SnapshotCopyTargetSQLRun) error {
	p.targetSQLCalls++
	if err := r.Validate(d); err != nil {
		return err
	}
	observed, err := p.FindSnapshotCopyTarget(ctx, d, r.Preparation)
	if err != nil {
		return err
	}
	if !observed.Prepared || p.targetSQLConnect == nil {
		return managedpostgres.ErrUnavailable
	}
	conn, err := p.targetSQLConnect(ctx, r.Target.DatabaseName)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var i managedpostgres.SnapshotCopyTargetSQLIdentity
	if err := conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int/10000,current_database(),current_user,d.oid,r.oid
 FROM pg_catalog.pg_database d,pg_catalog.pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user`).Scan(&i.PostgresMajor, &i.DatabaseName, &i.RoleName, &i.DatabaseOID, &i.RoleOID); err != nil {
		return managedpostgres.ErrUnavailable
	}
	if err := run(ctx, conn, i); err != nil {
		return err
	}
	p.targetSQLAfterCalls++
	return p.targetSQLAfterError
}

func cloneImportWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *importWorkerFailureStore, *cloneSnapshotProvider, capturedProjectEnvironmentDatabasePlan, copyinventory.ExportPlan, uint32, copyarchive.RestoreTarget, clonePostgresArchiveStorage, *archiveWorkerStorage, string) {
	t.Helper()
	f, p, source, exports, oid, _, artifact, b, tool := cloneOwnedReaderArchiveFixture(t)
	if _, err := f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), f.lease, source, exports, oid, artifact, 8<<20, archiveWorkerLimits(), tool, 4<<20); err != nil {
		t.Fatal(err)
	}
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &importWorkerFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	owned, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, source.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	var selected copyinventory.DatabaseExport
	for _, d := range requirements {
		if d.Database.OID == oid {
			selected = d
		}
	}
	name := "grg_import /?%é\n" + uuid.NewString()[:8]
	if _, err := f.pool.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg := f.pool.Config().ConnConfig.Copy()
	cfg.Database = name
	cfg.Password = "private-fixture-password"
	cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "off", "search_path": "pg_catalog"}
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	target := copyarchive.RestoreTarget{Scope: selected.Scope, OwnerID: owned.TargetDatabaseID, ProviderResourceID: owned.ProviderResourceID, ProviderCreatedAt: owned.ProviderCreatedAt,
		DataResourceID: owned.ProviderResourceID + "/br-target", EndpointID: "ep-copy-target", EndpointCreatedAt: owned.ProviderCreatedAt, DatabaseName: name, RoleName: cfg.User}
	err = conn.QueryRow(t.Context(), "SELECT d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user").Scan(&target.DatabaseOID, &target.RoleOID)
	_ = conn.Close(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p.targetSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected != name {
			return nil, managedpostgres.ErrConflict
		}
		return pgx.ConnectConfig(ctx, cfg.Copy())
	}
	return f, store, p, source, exports, oid, target, artifact, b, filepath.Join(filepath.Dir(tool), "pg_restore")
}

func importWorkerRow(t *testing.T, f cloneCoordinatorFixture, p *cloneSnapshotProvider, target copyarchive.RestoreTarget) (bool, string) {
	t.Helper()
	conn, err := p.targetSQLConnect(t.Context(), target.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var exists bool
	if err := conn.QueryRow(t.Context(), "SELECT to_regclass('public.selected_events') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		return false, ""
	}
	var note string
	if err := conn.QueryRow(t.Context(), "SELECT note FROM public.selected_events WHERE id=7").Scan(&note); err != nil {
		t.Fatal(err)
	}
	return true, note
}

func TestPGClonePostgresImportRealArchiveAndCommittedReceiptRecoveryNeverRepeatsSQL(t *testing.T) {
	f, store, p, source, exports, oid, target, artifact, b, tool := cloneImportWorkerFixture(t)
	store.loseRecord = true
	if e, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, tool, t.TempDir(), 4<<20); e != (copyarchive.RestoreExecution{}) || err == nil {
		t.Fatalf("lost receipt reply: %v", err)
	}
	owner, err := store.ProjectEnvironmentClonePostgresImportForLease(t.Context(), f.lease, source.source.ID, oid)
	if err != nil || owner.State != "executed" || p.targetSQLCalls != 1 || p.targetSQLAfterCalls != 1 {
		t.Fatalf("successful private materialization: %v", err)
	}
	if exists, note := importWorkerRow(t, f, p, target); !exists || note != "selected-reader-row" {
		t.Fatal("real archive did not restore selected data")
	}
	gets := b.gets
	f.srv.managedPostgres = nil
	e, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, "", t.TempDir(), 4<<20)
	if err != nil || !copyarchive.SameReceipt(e.Input, owner.Input) || !owner.MatchesTarget(e.Target) || p.targetSQLCalls != 1 || b.gets != gets || b.puts != 1 {
		t.Fatalf("receipt replay contacted SQL/storage or repeated a dump: %v", err)
	}
	conn, err := p.targetSQLConnect(t.Context(), target.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(t.Context(), "UPDATE public.selected_events SET note='target-only' WHERE id=7")
	_ = conn.Close(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reqs, _ := exports.RequirementsForWorker()
	var originalName string
	for _, d := range reqs {
		if d.Database.OID == oid {
			originalName = d.Database.Name
		}
	}
	conn, err = p.readerSelectedSQLConnect(t.Context(), originalName)
	if err != nil {
		t.Fatal(err)
	}
	var sourceNote string
	err = conn.QueryRow(t.Context(), "SELECT note FROM public.selected_events WHERE id=7").Scan(&sourceNote)
	_ = conn.Close(t.Context())
	if err != nil || sourceNote != "selected-reader-row" {
		t.Fatalf("target writes changed source: %v", err)
	}
}

func TestPGClonePostgresImportPostWriteProviderFailureHoldsUnknownOwnerAndCannotRepeat(t *testing.T) {
	f, store, p, source, exports, oid, target, artifact, b, tool := cloneImportWorkerFixture(t)
	p.targetSQLAfterError = errors.New("provider placement rejected after SQL commit")
	if e, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, tool, t.TempDir(), 4<<20); e != (copyarchive.RestoreExecution{}) || err == nil {
		t.Fatalf("post-write rejection became completion: %v", err)
	}
	owner, err := store.ProjectEnvironmentClonePostgresImportForLease(t.Context(), f.lease, source.source.ID, oid)
	if err != nil || owner.State != "importing" || !owner.ExecutedAt.IsZero() {
		t.Fatalf("uncertain SQL lost ownership: %v", err)
	}
	if exists, note := importWorkerRow(t, f, p, target); !exists || note != "selected-reader-row" {
		t.Fatal("fixture must commit before provider rejection")
	}
	gets := b.gets
	if _, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, tool, t.TempDir(), 4<<20); !errors.Is(err, managedpostgres.ErrUnavailable) || p.targetSQLCalls != 1 || b.gets != gets {
		t.Fatalf("unknown import repeated SQL/staging: %v", err)
	}
}

func TestPGClonePostgresImportCorruptInputBudgetAndLostClaimNeverWriteTarget(t *testing.T) {
	for _, fault := range []string{"corrupt_input", "spool_budget", "lost_claim"} {
		t.Run(fault, func(t *testing.T) {
			f, store, p, source, exports, oid, target, artifact, b, tool := cloneImportWorkerFixture(t)
			budget := int64(4 << 20)
			switch fault {
			case "lost_claim":
				store.loseClaim = true
			case "spool_budget":
				budget = 5
			case "corrupt_input":
				a, err := store.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), f.lease, source.source.ID, oid)
				if err != nil {
					t.Fatal(err)
				}
				r, err := b.StorageBackend.Get(t.Context(), a.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-1] ^= 1
				if err := b.StorageBackend.Put(t.Context(), a.StorageKey, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			if e, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, tool, t.TempDir(), budget); e != (copyarchive.RestoreExecution{}) || err == nil {
				t.Fatalf("unqualified import dispatched: %v", err)
			}
			owner, err := store.ProjectEnvironmentClonePostgresImportForLease(t.Context(), f.lease, source.source.ID, oid)
			if err != nil {
				t.Fatal(err)
			}
			if exists, _ := importWorkerRow(t, f, p, target); exists {
				t.Fatal("rejected input/claim wrote target SQL")
			}
			if fault == "lost_claim" {
				gets := b.gets
				if owner.State != "importing" || p.targetSQLCalls != 1 {
					t.Fatal("lost committed claim was forgotten")
				}
				if _, err := f.srv.projectEnvironmentClonePostgresImport(t.Context(), f.lease, source, exports, oid, target, artifact, tool, t.TempDir(), budget); err == nil || p.targetSQLCalls != 1 || b.gets != gets {
					t.Fatalf("lost claim redispatched SQL: %v", err)
				}
			} else if owner.State != "reserved" || p.targetSQLCalls != 0 {
				t.Fatal("unauthenticated input reached target SQL/claim")
			}
		})
	}
}
