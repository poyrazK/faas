//go:build !no_pg

// adr:568
package main

import (
	"bytes"
	"context"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/storage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

type importWorkerFailureStore struct {
	*state.PgStore
	loseClaim, loseRecord, loseReserve bool
}

func (s *importWorkerFailureStore) ReserveProjectEnvironmentClonePostgresImport(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresImportRequest) (state.ProjectEnvironmentClonePostgresImport, bool, error) {
	i, first, err := s.PgStore.ReserveProjectEnvironmentClonePostgresImport(ctx, l, r)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return i, false, managedpostgres.ErrUnavailable
	}
	return i, first, err
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
	defer func(cleanupCtx context.Context) { _ = conn.Close(context.WithoutCancel(cleanupCtx)) }(ctx)
	var i managedpostgres.SnapshotCopyTargetSQLIdentity
	if err := conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int/10000,current_database(),current_user,d.oid,r.oid
 FROM pg_catalog.pg_database d,pg_catalog.pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user`).Scan(&i.PostgresMajor, &i.DatabaseName, &i.RoleName, &i.DatabaseOID, &i.RoleOID); err != nil {
		return managedpostgres.ErrUnavailable
	}
	if err := run(ctx, conn, i); err != nil {
		return err
	}
	if p.targetSQLCheck != nil {
		if err := p.targetSQLCheck(ctx, conn, r.Target.DatabaseName); err != nil {
			return err
		}
	}
	p.targetSQLAfterCalls++
	return p.targetSQLAfterError
}

type importWorkerFixture struct {
	db                                 *databaseWorkerFixture
	store                              *importWorkerFailureStore
	sourceOID                          uint32
	target                             copyarchive.RestoreTarget
	artifact                           clonePostgresArchiveStorage
	backend                            *archiveWorkerStorage
	tool                               string
	childCalls                         int
	restored                           bool
	note, objectOwner                  string
	afterChild                         func(context.Context, *pgx.Conn) error
	childPostError, bootstrapPostError error
	bootstrapTracer                    pgx.QueryTracer
}

func cloneImportWorkerFixture(t *testing.T) *importWorkerFixture {
	return cloneImportWorkerFixtureWithPreparation(t, true)
}

func cloneImportWorkerFixtureWithPreparation(t *testing.T, prepare bool) *importWorkerFixture {
	t.Helper()
	dump := os.Getenv("FAAS_COPY_PG_DUMP")
	if dump == "" {
		t.Skip("FAAS_COPY_PG_DUMP required for real import contracts")
	}
	f := &importWorkerFixture{tool: filepath.Join(filepath.Dir(dump), "pg_restore")}
	name := "grg_import /?%é\n" + uuid.NewString()[:12]
	f.db = cloneDatabaseWorkerFixture(t, func(x *roleWorkerFixture) {
		if _, err := x.f.pool.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0 CONNECTION LIMIT 6"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := x.f.pool.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Error(err)
			}
		})
		cfg := x.f.pool.Config().ConnConfig.Copy()
		cfg.Database, cfg.Password = name, "source-fixture-password"
		conn, err := pgx.ConnectConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = conn.Exec(t.Context(), "CREATE TABLE public.selected_events(id int primary key,note text); INSERT INTO public.selected_events VALUES(7,'selected-reader-row'); ALTER TABLE public.selected_events OWNER TO "+pgx.Identifier{x.member}.Sanitize())
		_ = conn.Close(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	})
	x := f.db.x
	f.store = &importWorkerFailureStore{PgStore: x.store.PgStore}
	x.f.srv.store = f.store
	reqs, err := f.db.exports.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range reqs {
		if d.Database.Name == name {
			f.sourceOID = d.Database.OID
		}
	}
	if f.sourceOID == 0 {
		t.Fatal("missing original selected database")
	}
	sourceCfg := x.f.pool.Config().ConnConfig.Copy()
	sourceCfg.Password = "source-fixture-password"
	x.p.readerSelectedSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected != name {
			return nil, managedpostgres.ErrConflict
		}
		cfg := sourceCfg.Copy()
		cfg.Database = selected
		cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "search_path": "pg_catalog"}
		return pgx.ConnectConfig(ctx, cfg)
	}
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.backend = &archiveWorkerStorage{StorageBackend: local}
	f.artifact = clonePostgresArchiveStorage{ID: "private-import-artifacts", Fingerprint: strings.Repeat("f", 64), Backend: f.backend}
	if prepare {
		if _, err = x.f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, f.artifact, 8<<20, archiveWorkerLimits(), dump, 4<<20); err != nil {
			t.Fatal(err)
		}
		prepared, _, err := x.f.srv.projectEnvironmentClonePostgresDatabaseSQLPins(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID)
		if err != nil {
			t.Fatal(err)
		}
		f.target, err = prepared.TargetForWorker()
		if err != nil {
			t.Fatal(err)
		}
	}
	bootstrapConnect := x.p.targetSQLConnect
	x.p.targetSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected == x.target.DatabaseName {
			if f.bootstrapTracer == nil {
				return bootstrapConnect(ctx, selected)
			}
			cfg := x.targetRoot.Config().Copy()
			cfg.Database, cfg.Password = selected, "bootstrap-fixture-password"
			cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "off", "search_path": "pg_catalog"}
			cfg.Tracer = f.bootstrapTracer
			conn, err := pgx.ConnectConfig(ctx, cfg)
			if err == nil {
				x.connections = append(x.connections, conn)
			}
			return conn, err
		}
		if selected != f.target.DatabaseName {
			return nil, managedpostgres.ErrConflict
		}
		owner, err := f.store.ProjectEnvironmentClonePostgresImportForLease(ctx, x.f.lease, x.source.source.ID, f.sourceOID)
		if err != nil || owner.State != "importing" {
			return nil, errors.New("child borrow preceded durable import claim")
		}
		cfg := x.targetRoot.Config().Copy()
		cfg.Database, cfg.Password = selected, "target-fixture-password"
		cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "off", "search_path": "pg_catalog"}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			f.childCalls++
			x.connections = append(x.connections, conn)
		}
		return conn, err
	}
	// Fixture postchecks observe actual data before its synchronous borrower
	// closes the child; assertions never reopen closed database admission.
	x.p.targetSQLCheck = func(ctx context.Context, conn *pgx.Conn, selected string) error {
		if selected == x.target.DatabaseName {
			return f.bootstrapPostError
		}
		if err := conn.QueryRow(ctx, "SELECT to_regclass('public.selected_events') IS NOT NULL").Scan(&f.restored); err != nil {
			return err
		}
		if f.restored {
			if err := conn.QueryRow(ctx, "SELECT e.note,r.rolname FROM public.selected_events e,pg_class c,pg_roles r WHERE e.id=7 AND c.oid='public.selected_events'::regclass AND r.oid=c.relowner").Scan(&f.note, &f.objectOwner); err != nil {
				return err
			}
		}
		if f.afterChild != nil {
			if err := f.afterChild(ctx, conn); err != nil {
				return err
			}
		}
		return f.childPostError
	}
	x.p.targetSQLCalls, x.p.targetSQLAfterCalls = 0, 0
	return f
}

func (f *importWorkerFixture) run(t *testing.T) (copyarchive.RestoreExecution, error) {
	t.Helper()
	x := f.db.x
	return x.f.srv.projectEnvironmentClonePostgresImport(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, f.target, f.artifact, f.tool, t.TempDir(), 4<<20)
}
func (f *importWorkerFixture) owner(t *testing.T) state.ProjectEnvironmentClonePostgresImport {
	t.Helper()
	x := f.db.x
	o, err := f.store.ProjectEnvironmentClonePostgresImportForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

type importWorkerWindow struct {
	allow        bool
	state, owner string
	closedAt     *time.Time
}

func (f *importWorkerFixture) window(t *testing.T) importWorkerWindow {
	t.Helper()
	x := f.db.x
	cfg := x.targetRoot.Config().Copy()
	cfg.Database = x.target.DatabaseName
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.WithoutCancel(t.Context()))
	var w importWorkerWindow
	if err := conn.QueryRow(t.Context(), "SELECT d.datallowconn,w.state,w.owner_id::text,w.closed_at FROM gregale_copy_database_maintenance.windows w JOIN pg_database d ON d.oid=w.target_oid WHERE w.source_oid=$1::oid", f.sourceOID).Scan(&w.allow, &w.state, &w.owner, &w.closedAt); err != nil {
		t.Fatal(err)
	}
	return w
}

func (f *importWorkerFixture) assertClosed(t *testing.T, window bool) {
	t.Helper()
	x := f.db.x
	var closed bool
	if err := x.targetRoot.QueryRow(t.Context(), "SELECT NOT datallowconn AND NOT datistemplate AND datconnlimit=6 AND datacl IS NULL AND datdba=$2::oid FROM pg_database WHERE oid=$1::oid", f.target.DatabaseOID, f.target.RoleOID).Scan(&closed); err != nil || !closed {
		t.Fatal("original target admission/config not restored", err)
	}
	if window {
		w := f.window(t)
		if w.allow || w.state != "closed" || w.owner != f.owner(t).ImportID || w.closedAt == nil {
			t.Fatal("original window not closed")
		}
	}
	x.assertPrivate(t)
}

func TestPGClonePostgresImportRealArchiveAndCommittedReceiptRecoveryNeverRepeatsSQL(t *testing.T) {
	f := cloneImportWorkerFixture(t)
	x := f.db.x
	f.store.loseRecord = true
	f.afterChild = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "UPDATE public.selected_events SET note='target-only' WHERE id=7")
		return err
	}
	if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || err == nil {
		t.Fatal("lost receipt reply", err)
	}
	owner := f.owner(t)
	if owner.State != "executed" || x.p.targetSQLCalls != 2 || x.p.targetSQLAfterCalls != 2 || f.childCalls != 1 || !f.restored || f.note != "selected-reader-row" || f.objectOwner != x.member {
		t.Fatal("real original-owner archive was not restored exactly once")
	}
	f.assertClosed(t, true)
	gets := f.backend.gets
	old := mfaIdentities()[0]
	current, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, old} }
	setSecretRecipient = nil
	previous := x.f.lease
	if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), previous, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	x.f.lease, err = f.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.tool = ""
	f.artifact.Backend = nil
	e, err := f.run(t)
	if err != nil || !copyarchive.SameReceipt(e.Input, owner.Input) || e.Target != f.target || f.childCalls != 1 || x.p.targetSQLCalls != 3 || f.backend.gets != gets || f.backend.puts != 1 {
		t.Fatal("handoff replay repeated staging/restore or lost original pins", err)
	}
	f.assertClosed(t, true)
	conn, err := x.p.readerSelectedSQLConnect(t.Context(), f.target.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	var note string
	err = conn.QueryRow(t.Context(), "SELECT note FROM public.selected_events WHERE id=7").Scan(&note)
	_ = conn.Close(context.Background())
	if err != nil || note != "selected-reader-row" {
		t.Fatal("target writes changed source", err)
	}
}

func TestPGClonePostgresImportPostWriteProviderFailureHoldsUnknownOwnerAndCannotRepeat(t *testing.T) {
	for _, fault := range []string{"child post", "bootstrap post"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneImportWorkerFixture(t)
			x := f.db.x
			if fault == "child post" {
				f.childPostError = managedpostgres.ErrUnavailable
			} else {
				f.bootstrapPostError = managedpostgres.ErrUnavailable
			}
			if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || err == nil {
				t.Fatal("provider rejection became completion", err)
			}
			if owner := f.owner(t); owner.State != "importing" || !owner.ExecutedAt.IsZero() || !f.restored {
				t.Fatal("uncertain committed SQL lost ownership")
			}
			f.assertClosed(t, true)
			gets := f.backend.gets
			f.childPostError, f.bootstrapPostError = nil, nil
			if _, err := f.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || f.childCalls != 1 || x.p.targetSQLCalls != 3 || f.backend.gets != gets {
				t.Fatal("unknown import repeated SQL/staging", err)
			}
			f.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresImportCorruptInputBudgetAndLostClaimNeverWriteTarget(t *testing.T) {
	for _, fault := range []string{"corrupt input", "spool budget", "lost claim", "lost reserve"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneImportWorkerFixture(t)
			x := f.db.x
			budget := int64(4 << 20)
			switch fault {
			case "lost claim":
				f.store.loseClaim = true
			case "lost reserve":
				f.store.loseReserve = true
			case "spool budget":
				budget = 5
			case "corrupt input":
				a, err := f.store.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
				if err != nil {
					t.Fatal(err)
				}
				r, err := f.backend.StorageBackend.Get(t.Context(), a.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-1] ^= 1
				if err := f.backend.StorageBackend.Put(t.Context(), a.StorageKey, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			e, err := x.f.srv.projectEnvironmentClonePostgresImport(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, f.target, f.artifact, f.tool, t.TempDir(), budget)
			if e != (copyarchive.RestoreExecution{}) || err == nil || f.childCalls != 0 {
				t.Fatal("unqualified input/claim wrote target", err)
			}
			owner := f.owner(t)
			f.assertClosed(t, false)
			if fault == "lost claim" {
				gets := f.backend.gets
				if owner.State != "importing" || x.p.targetSQLCalls != 1 {
					t.Fatal("lost committed claim was forgotten")
				}
				if _, err := f.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || f.childCalls != 0 || x.p.targetSQLCalls != 2 || f.backend.gets != gets {
					t.Fatal("lost claim redispatched SQL", err)
				}
			} else if owner.State != "reserved" || x.p.targetSQLCalls != 0 {
				t.Fatal("input/reserve rejection reached target SQL/claim")
			}
		})
	}
}

// Drop only this fixture's borrower after the admission transaction has really
// committed, before the worker can observe a usable opening/closure result.
type importWorkerLostWindowReply struct {
	phase, armed string
	fired        bool
}
type importWorkerCommitKey struct{}

func (tr *importWorkerLostWindowReply) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_temp.gregale_copy_database_maintenance_change(") && len(d.Args) > 7 {
		if action, ok := d.Args[7].(string); ok {
			tr.armed = action
		}
	}
	return context.WithValue(ctx, importWorkerCommitKey{}, strings.EqualFold(strings.TrimSpace(d.SQL), "commit"))
}
func (tr *importWorkerLostWindowReply) TraceQueryEnd(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(importWorkerCommitKey{}).(bool); commit && d.Err == nil && tr.armed == tr.phase && !tr.fired {
		tr.fired = true
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = c.PgConn().Close(cleanup)
	}
}
func TestPGClonePostgresImportLostWindowReplyHandoffClosesOriginalWithoutRestore(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f := cloneImportWorkerFixture(t)
			x := f.db.x
			tr := &importWorkerLostWindowReply{phase: phase}
			f.bootstrapTracer = tr
			if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || err == nil || !tr.fired {
				t.Fatal("lost committed maintenance reply became execution", err)
			}
			original := f.owner(t)
			w := f.window(t)
			allow, status, owner, closedAt := w.allow, w.state, w.owner, w.closedAt
			if original.State != "importing" || owner != original.ImportID {
				t.Fatal("unknown maintenance lost original import ownership")
			}
			if phase == "open" && (!allow || status != "open" || f.childCalls != 0) {
				t.Fatal("unknown opening ran restore or forgot open admission")
			}
			if phase == "finish" && (allow || status != "closed" || f.childCalls != 1 || closedAt == nil) {
				t.Fatal("unknown closure forgot committed restore/closure")
			}
			// Strict closed-catalogue verification would reject the opening case here;
			// handoff must open retained metadata and close the original window first.
			previous := x.f.lease
			if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), previous, 0); err != nil {
				t.Fatal(err)
			}
			var err error
			x.f.lease, err = f.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), 2*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			calls, gets := f.childCalls, f.backend.gets
			f.bootstrapTracer = nil
			f.tool = ""
			f.artifact.Backend = nil
			if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || !errors.Is(err, managedpostgres.ErrUnavailable) || f.childCalls != calls || f.backend.gets != gets {
				t.Fatal("unknown import retried dump/staging/restore", err)
			}
			if got := f.owner(t); got != original {
				t.Fatal("closure recovery changed import outcome")
			}
			f.assertClosed(t, true)
			if closedAt != nil {
				recovered := f.window(t).closedAt
				if recovered == nil || !recovered.Equal(*closedAt) {
					t.Fatal("handoff replaced original closure time")
				}
			}
		})
	}
}

func TestPGClonePostgresImportLeakedChildRemainsClosedUntilCloseOnlyRecovery(t *testing.T) {
	f := cloneImportWorkerFixture(t)
	x := f.db.x
	var leak *pgx.Conn
	f.afterChild = func(ctx context.Context, _ *pgx.Conn) error {
		cfg := x.targetRoot.Config().Copy()
		cfg.Database = f.target.DatabaseName
		var err error
		leak, err = pgx.ConnectConfig(ctx, cfg)
		return err
	}
	t.Cleanup(func() {
		if leak != nil {
			_ = leak.Close(context.Background())
		}
	})
	if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || err == nil || leak == nil {
		t.Fatal("leaked child gave successful closure", err)
	}
	w := f.window(t)
	if w.allow || w.state != "closing" {
		t.Fatal("leaked child left database open")
	}
	gets := f.backend.gets
	if _, err := f.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || f.childCalls != 1 || f.backend.gets != gets {
		t.Fatal("leaked recovery repeated import", err)
	}
	if err := leak.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) || f.childCalls != 1 || f.backend.gets != gets {
		t.Fatal("closed leak recovery repeated import", err)
	}
	f.assertClosed(t, true)
}

func TestPGClonePostgresImportRejectsOriginalInputAndOwnershipChangesBeforeIO(t *testing.T) {
	for _, fault := range []string{"target", "missing child", "legacy", "parent", "archive", "original key", "stale", "phase"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneImportWorkerFixture(t)
			x := f.db.x
			target := f.target
			switch fault {
			case "target":
				target.DatabaseOID++
			case "missing child":
				if _, err := x.f.pool.Exec(t.Context(), "DELETE FROM project_environment_clone_postgres_database_sql_pins WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				a, err := f.store.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = f.store.ReserveProjectEnvironmentClonePostgresImport(t.Context(), x.f.lease, state.ProjectEnvironmentClonePostgresImportRequest{Input: a.Receipt, Target: target}); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_database_plans SET ciphertext_sha256=repeat('f',64) WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "archive":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=repeat('d',64) WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "original key":
				other, _ := age.GenerateX25519Identity()
				mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{other} }
			case "stale":
				if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), x.f.lease, 0); err != nil {
					t.Fatal(err)
				}
			case "phase":
				if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			}
			gets := f.backend.gets
			e, err := x.f.srv.projectEnvironmentClonePostgresImport(t.Context(), x.f.lease, x.source, f.db.exports, f.sourceOID, target, f.artifact, f.tool, t.TempDir(), 4<<20)
			if e != (copyarchive.RestoreExecution{}) || err == nil || x.p.targetSQLCalls != 0 || f.backend.gets != gets {
				t.Fatal("changed original prerequisites reached storage/SQL", err)
			}
			f.assertClosed(t, false)
		})
	}
}

func TestPGClonePostgresImportExecutedReplayRequiresOriginalClosureAndProviderPostchecks(t *testing.T) {
	for _, fault := range []string{"database drift", "missing window", "window owner", "provider post"} {
		t.Run(fault, func(t *testing.T) {
			f := cloneImportWorkerFixture(t)
			x := f.db.x
			if _, err := f.run(t); err != nil {
				t.Fatal(err)
			}
			original := f.owner(t)
			gets := f.backend.gets
			if fault == "database drift" {
				if _, err := x.targetRoot.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{f.target.DatabaseName}.Sanitize()+" CONNECTION LIMIT 19"); err != nil {
					t.Fatal(err)
				}
			} else if fault == "provider post" {
				f.bootstrapPostError = managedpostgres.ErrUnavailable
			} else {
				cfg := x.targetRoot.Config().Copy()
				cfg.Database = x.target.DatabaseName
				conn, err := pgx.ConnectConfig(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				query := "DELETE FROM gregale_copy_database_maintenance.windows WHERE source_oid=$1::oid"
				if fault == "window owner" {
					query = "UPDATE gregale_copy_database_maintenance.windows SET owner_id='" + uuid.NewString() + "'::uuid WHERE source_oid=$1::oid"
				}
				_, err = conn.Exec(t.Context(), query, f.sourceOID)
				_ = conn.Close(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			}
			f.tool = ""
			if e, err := f.run(t); e != (copyarchive.RestoreExecution{}) || err == nil || f.childCalls != 1 || f.backend.gets != gets {
				t.Fatal("stored command bypassed original closure/placement proof", err)
			}
			if got := f.owner(t); got != original {
				t.Fatal("failed replay erased executed ownership")
			}
			if fault == "database drift" {
				if _, err := x.targetRoot.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{f.target.DatabaseName}.Sanitize()+" CONNECTION LIMIT 6"); err != nil {
					t.Fatal(err)
				}
			}
			f.assertClosed(t, false)
		})
	}
}
