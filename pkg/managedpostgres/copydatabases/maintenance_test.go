// adr:566
package copydatabases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func maintenanceChild(ctx context.Context, t *testing.T, f *fixture, target copyarchive.RestoreTarget) *pgx.Conn {
	t.Helper()
	cfg := f.target.Config().Copy()
	cfg.Database = target.DatabaseName
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func maintenanceStatus(ctx context.Context, t *testing.T, f *fixture, id uint32) (bool, bool, int32, string) {
	t.Helper()
	var allow, template bool
	var limit int32
	var state string
	d, _, err := f.plan.creationDatabase(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.targetRoot.QueryRow(ctx, "SELECT datallowconn,datistemplate,datconnlimit FROM pg_database WHERE oid=$1", d.OID).Scan(&allow, &template, &limit); err != nil {
		t.Fatal(err)
	}
	cfg := f.targetRoot.Config().Copy()
	cfg.Database = f.bootstrap
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.WithoutCancel(ctx))
	var exists bool
	if err = c.QueryRow(ctx, "SELECT to_regclass('gregale_copy_database_maintenance.windows') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		err = c.QueryRow(ctx, "SELECT state FROM gregale_copy_database_maintenance.windows WHERE source_oid=$1", id).Scan(&state)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
	}
	return allow, template, limit, state
}
func assertMaintenanceOriginal(t *testing.T, f *fixture, r Receipt) {
	t.Helper()
	if err := r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); err != nil {
		t.Fatal("original catalogue changed", err)
	}
}
func existingClosedTemplate(t *testing.T, f *fixture) {
	t.Helper()
	run(t.Context(), t, f.targetRoot, "CREATE DATABASE "+pgx.Identifier{f.template}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()+" TEMPLATE template0 LOCALE_PROVIDER icu ICU_LOCALE 'und' ICU_RULES '&a < b' IS_TEMPLATE true ALLOW_CONNECTIONS false CONNECTION LIMIT 0")
}

func TestCopyDatabaseMaintenancePreservesClosedZeroLimitAndUnsetACL(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_%t", existing), func(t *testing.T) {
			f := newFixtureConfigured(t, func(f *fixture) {
				if existing {
					existingClosedTemplate(t, f)
				} else {
					run(t.Context(), t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT 0")
				}
			})
			id := f.ordinaryOID
			if existing {
				id = f.templateOID
			}
			r := f.prepare(t, id)
			d, _, _ := f.plan.creationDatabase(id)
			if d.ACL != nil {
				t.Fatal("unset ACL fixture lost NULL")
			}
			dispatch, calls := uuid.New(), 0
			closure, err := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
				calls++
				allow, template, limit, state := maintenanceStatus(ctx, t, f, id)
				if !allow || template || limit != api.PostgresCopyMaintenanceConnections || state != "open" {
					t.Fatal("worker admission not isolated")
				}
				c := maintenanceChild(ctx, t, f, target)
				defer c.Close(context.WithoutCancel(ctx))
				second := maintenanceChild(ctx, t, f, target)
				defer second.Close(context.WithoutCancel(ctx))
				thirdCfg := c.Config().Copy()
				third, e := pgx.ConnectConfig(ctx, thirdCfg)
				if third != nil {
					third.Close(context.WithoutCancel(ctx))
				}
				if e == nil {
					t.Fatal("temporary connection limit was not enforced")
				}
				cfg := c.Config().Copy()
				cfg.User = f.dataOwner
				customer, e := pgx.ConnectConfig(ctx, cfg)
				if customer != nil {
					customer.Close(context.WithoutCancel(ctx))
				}
				if e == nil {
					t.Fatal("NOLOGIN customer admitted")
				}
				return nil
			})
			if err != nil || calls != 1 || closure.ClosedAt().IsZero() {
				t.Fatalf("private maintenance: %v", err)
			}
			assertMaintenanceOriginal(t, f, r)
			allow, template, limit, state := maintenanceStatus(t.Context(), t, f, id)
			if allow || template != d.Template || limit != d.ConnectionLimit || state != "closed" {
				t.Fatal("original admission/template/limit not restored")
			}
			again, err := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
			if err != nil || !again.ClosedAt().Equal(closure.ClosedAt()) {
				t.Fatal("closure timestamp regenerated", err)
			}
			if got, err := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }); got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrConflict) || calls != 1 {
				t.Fatal("terminal dispatch replayed", err)
			}
			if got, err := r.CloseMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize); got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatal("different dispatch adopted window", err)
			}
			raw, _ := json.Marshal(closure)
			for _, display := range []string{string(raw), fmt.Sprint(closure), fmt.Sprintf("%#v", closure)} {
				for _, secret := range []string{d.Name, f.owner, dispatch.String(), f.pins.ProviderResourceID, f.pins.EndpointID} {
					if strings.Contains(display, secret) {
						t.Fatal("closure leaked private ownership")
					}
				}
			}
		})
	}
}

func TestCopyDatabaseMaintenanceRejectsOtherLoginAndOwnerAssumption(t *testing.T) {
	for _, membership := range []bool{false, true} {
		t.Run(fmt.Sprintf("membership_%t", membership), func(t *testing.T) {
			f := newFixtureConfigured(t, func(f *fixture) {
				for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
					run(t.Context(), t, root, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" LOGIN NOINHERIT")
				}
				if membership {
					existingClosedTemplate(t, f)
					run(t.Context(), t, f.targetRoot, "REVOKE ALL ON DATABASE "+pgx.Identifier{f.template}.Sanitize()+" FROM PUBLIC")
					run(t.Context(), t, f.targetRoot, "GRANT "+pgx.Identifier{f.owner}.Sanitize()+" TO "+pgx.Identifier{f.dataOwner}.Sanitize()+" WITH INHERIT FALSE, SET TRUE")
				}
			})
			id := f.ordinaryOID
			if membership {
				id = f.templateOID
			}
			r := f.prepare(t, id)
			calls := 0
			got, err := r.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
			if got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrUnsupported) || calls != 0 {
				t.Fatalf("foreign login admitted: %v", err)
			}
			allow, _, _, state := maintenanceStatus(t.Context(), t, f, id)
			if allow || state != "" {
				t.Fatal("unsupported admission left a window")
			}
			assertMaintenanceOriginal(t, f, r)
			// Memberships are independent of role seed attributes; cleanup the explicit
			// fixture membership before its role teardown.
			if membership {
				run(t.Context(), t, f.targetRoot, "REVOKE "+pgx.Identifier{f.owner}.Sanitize()+" FROM "+pgx.Identifier{f.dataOwner}.Sanitize())
			}
		})
	}
}

func TestCopyDatabaseMaintenanceFailureCancellationAndExpiredLeaseClose(t *testing.T) {
	for _, mode := range []string{"failure", "cancelled", "expired_after_callback", "expired_open_transaction"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			dispatch := uuid.New()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			expired := false
			calls := 0
			authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				if expired || (mode == "expired_open_transaction" && f.target.PgConn().TxStatus() == 'T') {
					return fmt.Errorf("private-dispatch-token: %w", pgerrors.ErrConflict)
				}
				return f.authorize(ctx, target)
			}
			got, err := r.WithMaintenance(ctx, f.target, f.exports, dispatch, authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
				calls++
				c := maintenanceChild(ctx, t, f, target)
				defer c.Close(context.WithoutCancel(ctx))
				run(ctx, t, c, "CREATE TABLE public.committed_before_worker_reply(id integer)")
				if e := c.Close(context.WithoutCancel(ctx)); e != nil {
					t.Fatal(e)
				}
				switch mode {
				case "failure":
					return errors.New("private-worker-credentials")
				case "cancelled":
					cancel()
					return ctx.Err()
				case "expired_after_callback":
					expired = true
				}
				return nil
			})
			want := pgerrors.ErrUnavailable
			if mode == "cancelled" {
				want = context.Canceled
			}
			if strings.HasPrefix(mode, "expired") {
				want = pgerrors.ErrConflict
			}
			if got != (MaintenanceClosure{}) || !errors.Is(err, want) || strings.Contains(fmt.Sprint(err), "private-") {
				t.Fatal("uncertain write supplied closure or diagnostic", err)
			}
			allow, _, _, state := maintenanceStatus(t.Context(), t, f, f.ordinaryOID)
			if allow {
				t.Fatal("failed dispatch left admission open")
			}
			if mode == "expired_open_transaction" {
				if calls != 0 || state != "" {
					t.Fatal("rejected transaction committed window")
				}
			} else {
				if calls != 1 || state != "closed" {
					t.Fatal("cleanup depended on expired dispatch authority")
				}
				recovered, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
				if e != nil || recovered.ClosedAt().IsZero() {
					t.Fatal("original close-only recovery failed", e)
				}
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

func TestCopyDatabaseMaintenanceRecoversCrashedBorrowerWithoutCallback(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	dispatch := uuid.New()
	m, err := newMaintenance(r, f.target, f.exports, dispatch, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = m.open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = f.target.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := f.targetRoot.Config().Copy()
	cfg.User, cfg.Database = f.owner, f.bootstrap
	cfg.RuntimeParams = map[string]string{"search_path": "pg_catalog"}
	f.target, err = pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatal("open window passed original preparation verification", err)
	}
	calls := 0
	got, err := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
	if got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrConflict) || calls != 0 {
		t.Fatal("crashed window replayed import", err)
	}
	closure, err := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
	if err != nil || closure.ClosedAt().IsZero() {
		t.Fatal("crashed window closure not recovered", err)
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseMaintenanceQuiescesBeforeSessionAndCatalogueFailure(t *testing.T) {
	for _, mode := range []string{"worker_session", "foreign_session", "role_drift", "database_drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			dispatch := uuid.New()
			var leaked *pgx.Conn
			got, err := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
				switch mode {
				case "worker_session":
					leaked = maintenanceChild(ctx, t, f, target)
				case "foreign_session":
					cfg := f.targetRoot.Config().Copy()
					cfg.Database = target.DatabaseName
					var e error
					leaked, e = pgx.ConnectConfig(ctx, cfg)
					if e != nil {
						t.Fatal(e)
					}
				case "role_drift":
					run(ctx, t, f.targetRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" LOGIN")
				case "database_drift":
					run(ctx, t, f.targetRoot, "ALTER DATABASE template1 CONNECTION LIMIT 23")
				}
				return nil
			})
			if got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatal("drift/leaked session returned closure", err)
			}
			allow, template, limit, state := maintenanceStatus(t.Context(), t, f, f.ordinaryOID)
			if allow || template || limit != api.PostgresCopyMaintenanceConnections || state != "closing" {
				t.Fatal("closure verified before quiescing")
			}
			if leaked != nil {
				var alive bool
				if e := leaked.QueryRow(t.Context(), "SELECT true").Scan(&alive); e != nil || !alive {
					t.Fatal("cleanup terminated a borrowed session", e)
				}
				if e := leaked.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "role_drift" {
				run(t.Context(), t, f.targetRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" NOLOGIN")
			}
			if mode == "database_drift" {
				run(t.Context(), t, f.targetRoot, "ALTER DATABASE template1 CONNECTION LIMIT -1")
			}
			closure, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
			if e != nil || closure.ClosedAt().IsZero() {
				t.Fatal("quiesced window did not recover", e)
			}
			assertMaintenanceOriginal(t, f, r)
		})
	}
}

func TestCopyDatabaseMaintenanceRejectsMissingAndChangedOwnershipWithoutRepair(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	dispatch := uuid.New()
	if got, err := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize); got != (MaintenanceClosure{}) || !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatal("missing window fabricated closure", err)
	}
	_, _, _, state := maintenanceStatus(t.Context(), t, f, f.ordinaryOID)
	if state != "" {
		t.Fatal("missing recovery installed journal")
	}
	closure, err := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{"timestamp", "plan", "shared", "extra_relation", "missing_active_index"} {
		t.Run(damage, func(t *testing.T) {
			tx, e := f.target.Begin(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			switch damage {
			case "timestamp":
				_, e = tx.Exec(t.Context(), "UPDATE gregale_copy_database_maintenance.windows SET preparation_created_at=preparation_created_at-interval '1 second'")
			case "plan":
				_, e = tx.Exec(t.Context(), "UPDATE gregale_copy_database_maintenance.windows SET plan_fingerprint=repeat('f',64)")
			case "shared":
				_, e = tx.Exec(t.Context(), "GRANT USAGE ON SCHEMA gregale_copy_database_maintenance TO PUBLIC")
			case "extra_relation":
				_, e = tx.Exec(t.Context(), "CREATE TABLE gregale_copy_database_maintenance.extra(id integer)")
			case "missing_active_index":
				_, e = tx.Exec(t.Context(), "DROP INDEX gregale_copy_database_maintenance.windows_one_active")
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(t.Context()); e != nil {
				t.Fatal(e)
			}
			got, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
			if got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrConflict) {
				t.Fatal("damaged journal accepted/repaired", e)
			}
			switch damage {
			case "timestamp":
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_maintenance.windows SET preparation_created_at=$1", r.createdAt)
			case "plan":
				fp, _ := preparationPlanFingerprint(r.plan)
				run(t.Context(), t, f.target, "UPDATE gregale_copy_database_maintenance.windows SET plan_fingerprint=$1", fp)
			case "shared":
				run(t.Context(), t, f.target, "REVOKE USAGE ON SCHEMA gregale_copy_database_maintenance FROM PUBLIC")
			case "extra_relation":
				run(t.Context(), t, f.target, "DROP TABLE gregale_copy_database_maintenance.extra")
			case "missing_active_index":
				run(t.Context(), t, f.target, "CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_maintenance.windows ((1)) WHERE state IN ('open','closing')")
			}
		})
	}
	again, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
	if e != nil || !again.ClosedAt().Equal(closure.ClosedAt()) {
		t.Fatal("repair fixture lost original closure time", e)
	}
}

func TestCopyDatabaseMaintenanceStaleLockWaiterAndConcurrentOriginalDispatch(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	dispatch := uuid.New()
	cfg := f.target.Config().Copy()
	other, e := pgx.ConnectConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	completed := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		_, e := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error {
			calls.Add(1)
			close(entered)
			<-release
			return nil
		})
		completed <- e
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first window never entered")
	}
	waiting := make(chan error, 1)
	var stale atomic.Bool
	authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
		if stale.Load() {
			return pgerrors.ErrConflict
		}
		return f.authorize(ctx, target)
	}
	go func() {
		_, e := r.WithMaintenance(t.Context(), other, f.exports, dispatch, authorize, func(context.Context, copyarchive.RestoreTarget) error { calls.Add(1); return nil })
		waiting <- e
	}()
	// Observe the actual waiting backend rather than a sleep-based race assertion.
	waitCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var blocked bool
		if e = f.targetRoot.QueryRow(waitCtx, "SELECT wait_event='advisory' FROM pg_stat_activity WHERE pid=$1", other.PgConn().PID()).Scan(&blocked); e == nil && blocked {
			break
		}
		if waitCtx.Err() != nil {
			close(release)
			t.Fatal("second worker never waited", e)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stale.Store(true)
	close(release)
	if e = <-completed; e != nil {
		t.Fatal("first window failed", e)
	}
	if e = <-waiting; !errors.Is(e, pgerrors.ErrConflict) || calls.Load() != 1 {
		t.Fatal("stale waiter executed callback", e)
	}
	got, e := r.WithMaintenance(t.Context(), other, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls.Add(1); return nil })
	if got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls.Load() != 1 {
		t.Fatal("original dispatch replayed after lock", e)
	}
	assertMaintenanceOriginal(t, f, r)
}

// This backend exists only within the private local fixture. No cloud calls.
type maintenanceArchiveBackend struct{ body []byte }

func (b *maintenanceArchiveBackend) Put(_ context.Context, _ string, r io.Reader) error {
	body, e := io.ReadAll(r)
	if e == nil {
		b.body = body
	}
	return e
}
func (b *maintenanceArchiveBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

func TestCopyDatabaseMaintenanceRestoresAuthenticatedArchiveAndIsolatesData(t *testing.T) {
	tool := os.Getenv("FAAS_COPY_PG_DUMP")
	if !filepath.IsAbs(tool) {
		t.Skip("explicit local PostgreSQL 16 pg_dump required")
	}
	f := newFixtureConfigured(t, func(f *fixture) {
		// Ordinary creator can restore original object ownership without superuser
		// or owner suppression. This explicit local authority is not provider proof.
		for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
			run(t.Context(), t, root, "GRANT "+pgx.Identifier{f.dataOwner}.Sanitize()+" TO "+pgx.Identifier{f.owner}.Sanitize()+" WITH INHERIT TRUE, SET TRUE")
		}
		cfg := f.sourceRoot.Config().Copy()
		cfg.Database = f.ordinary
		c, e := pgx.ConnectConfig(t.Context(), cfg)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close(context.WithoutCancel(t.Context()))
		run(t.Context(), t, c, "SET ROLE "+pgx.Identifier{f.dataOwner}.Sanitize())
		run(t.Context(), t, c, "CREATE SCHEMA app; CREATE TABLE app.events(id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,value text NOT NULL); INSERT INTO app.events(value) VALUES ('original production row'); CREATE FUNCTION app.count_events() RETURNS bigint LANGUAGE SQL AS 'SELECT count(*) FROM app.events'; REVOKE ALL ON TABLE app.events FROM PUBLIC")
	})
	r := f.prepare(t, f.ordinaryOID)
	dispatch := uuid.New()
	requirements, _ := f.exports.RequirementsForWorker()
	var requirement copyinventory.DatabaseExport
	for _, d := range requirements {
		if d.Database.OID == f.ordinaryOID {
			requirement = d
		}
	}
	cfg := f.source.Config().Copy()
	cfg.Database = f.ordinary
	cfg.Password = "private-source-fixture-password"
	cfg.RuntimeParams = map[string]string{"search_path": "pg_catalog", "default_transaction_read_only": "on"}
	source, e := pgx.ConnectConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close(context.Background())
	id, _ := age.GenerateX25519Identity()
	backend := &maintenanceArchiveBackend{}
	key := "postgres-copies/" + requirement.Scope.OperationID + "/" + uuid.NewString() + ".age"
	archive, e := copyarchive.Upload(t.Context(), backend, key, requirement, []*age.X25519Identity{id}, 8<<20, func(ctx context.Context, w io.Writer) (copyarchive.Receipt, error) {
		return copyarchive.Export(ctx, source, requirement, tool, id.Recipient(), w, 4<<20)
	})
	if e != nil {
		t.Fatal("authenticated export", e)
	}
	staged, e := copyarchive.StageRetained(t.Context(), backend, key, requirement, []*age.X25519Identity{id}, archive, t.TempDir(), 4<<20, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer staged.Close()
	var execution copyarchive.RestoreExecution
	closure, e := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(ctx context.Context, target copyarchive.RestoreTarget) error {
		cfg := f.target.Config().Copy()
		cfg.Database, cfg.Password = target.DatabaseName, "private-target-fixture-password"
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return err
		}
		defer c.Close(context.WithoutCancel(ctx))
		placement := func(ctx context.Context, conn *pgx.Conn, got copyarchive.RestoreTarget) error {
			if conn != c || got != target || conn.Config().Host != f.targetRoot.Config().Host || conn.Config().Host == source.Config().Host {
				return pgerrors.ErrConflict
			}
			return nil
		}
		var e error
		execution, e = staged.Restore(ctx, c, target, filepath.Join(filepath.Dir(tool), "pg_restore"), placement)
		if e != nil {
			return e
		}
		var owner, value string
		var count int64
		if e = c.QueryRow(ctx, "SELECT pg_get_userbyid(relowner)::text FROM pg_class WHERE oid='app.events'::regclass").Scan(&owner); e != nil {
			return e
		}
		if owner != f.dataOwner {
			return pgerrors.ErrConflict
		}
		if e = c.QueryRow(ctx, "SELECT value,app.count_events() FROM app.events WHERE id=1").Scan(&value, &count); e != nil {
			return e
		}
		if value != "original production row" || count != 1 {
			return pgerrors.ErrConflict
		}
		if _, e = c.Exec(ctx, "INSERT INTO app.events(value) VALUES ('stage-only row')"); e != nil {
			return e
		}
		if e = c.QueryRow(ctx, "SELECT app.count_events()").Scan(&count); e != nil || count != 2 {
			return pgerrors.ErrConflict
		}
		return nil
	})
	if e != nil || closure.ClosedAt().IsZero() || !copyarchive.SameReceipt(execution.Input, archive) {
		t.Fatal("closed target archive restore", e)
	}
	var count int64
	if e = source.QueryRow(t.Context(), "SELECT app.count_events()").Scan(&count); e != nil || count != 1 {
		t.Fatal("target writes reached production", e)
	}
	verificationOwner := uuid.New()
	verificationClosure, e := r.WithVerificationAccess(t.Context(), f.target, f.exports, dispatch, verificationOwner, f.authorize, func(ctx context.Context, access VerificationTarget) error {
		target, e := access.TargetForWorker()
		if e != nil {
			return e
		}
		c := maintenanceChild(ctx, t, f, target)
		defer c.Close(context.WithoutCancel(ctx))
		return access.WithReadOnly(ctx, c, verificationPlacement(f, c, target), func(ctx context.Context, tx pgx.Tx) error {
			var owner, original, stage string
			var rows int64
			if e := tx.QueryRow(ctx, "SELECT pg_get_userbyid(relowner)::text FROM pg_class WHERE oid='app.events'::regclass").Scan(&owner); e != nil {
				return e
			}
			if e := tx.QueryRow(ctx, "SELECT value,app.count_events() FROM app.events WHERE id=1").Scan(&original, &rows); e != nil {
				return e
			}
			if e := tx.QueryRow(ctx, "SELECT value FROM app.events WHERE id=2").Scan(&stage); e != nil {
				return e
			}
			if owner != f.dataOwner || original != "original production row" || stage != "stage-only row" || rows != 2 {
				return pgerrors.ErrConflict
			}
			return nil
		})
	})
	if e != nil || verificationClosure.ClosedAt().IsZero() {
		t.Fatal("separate authenticated archive inspection", e)
	}
	if original, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize); e != nil || original != closure {
		t.Fatal("archive inspection changed import closure", e)
	}
	assertMaintenanceOriginal(t, f, r)
	// All fixture metadata still has the original full plan; this successful
	// import provides no independent equivalence or complete stage readiness.
	if !reflect.DeepEqual(r.plan.body, f.plan.body) {
		t.Fatal("import changed frozen plan")
	}
}

func TestCopyDatabaseMaintenanceSerializesDifferentDatabasesUntilOriginalClosure(t *testing.T) {
	f := newFixture(t)
	first, second := f.prepare(t, f.ordinaryOID), f.prepare(t, f.templateOID)
	dispatch := uuid.New()
	m, e := newMaintenance(first, f.target, f.exports, dispatch, f.authorize)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.lock(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = m.open(t.Context()); e != nil {
		t.Fatal(e)
	}
	// Simulate loss of the original dispatch process after the atomic opening.
	releaseLock(t.Context(), f.target, m.q)
	calls := 0
	got, e := second.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
	if got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls != 0 {
		t.Fatal("second database opened before original closure", e)
	}
	allow, _, _, state := maintenanceStatus(t.Context(), t, f, f.templateOID)
	if allow || state != "" {
		t.Fatal("blocked database retained a new window")
	}
	if _, e = first.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize); e != nil {
		t.Fatal(e)
	}
	if _, e = second.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }); e != nil || calls != 1 {
		t.Fatal("second database did not advance after closure", e)
	}
	assertMaintenanceOriginal(t, f, first)
	assertMaintenanceOriginal(t, f, second)
}

func TestCopyDatabaseMaintenanceValidatesIdentityScopeAndAdmissionBeforeMutation(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	for _, mode := range []string{"nil_callback", "nil_authority", "zero_dispatch", "dispatch_alias", "empty_receipt", "changed_preparation", "missing_source", "nil_connection", "wrong_database"} {
		t.Run(mode, func(t *testing.T) {
			input, source, conn, dispatch, authorize := r, f.exports, f.target, uuid.New(), f.authorize
			calls := 0
			var run MaintenanceRun = func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil }
			want := pgerrors.ErrConflict
			switch mode {
			case "nil_callback":
				run = nil
				want = pgerrors.ErrInvalid
			case "nil_authority":
				authorize = nil
				want = pgerrors.ErrInvalid
			case "zero_dispatch":
				dispatch = uuid.Nil
				want = pgerrors.ErrInvalid
			case "dispatch_alias":
				dispatch = uuid.MustParse(f.pins.OwnerID)
				want = pgerrors.ErrInvalid
			case "empty_receipt":
				input = Receipt{}
			case "changed_preparation":
				input.createdAt = input.createdAt.Add(-time.Second)
			case "missing_source":
				source = copyinventory.ExportPlan{}
			case "nil_connection":
				conn = nil
			case "wrong_database":
				conn = f.targetRoot
			}
			got, e := input.WithMaintenance(t.Context(), conn, source, dispatch, authorize, run)
			if got != (MaintenanceClosure{}) || !errors.Is(e, want) || calls != 0 {
				t.Fatal("invalid maintenance input reached callback", e)
			}
			allow, _, _, state := maintenanceStatus(t.Context(), t, f, f.ordinaryOID)
			if allow || state != "" {
				t.Fatal("invalid input changed target admission")
			}
		})
	}
	assertMaintenanceOriginal(t, f, r)
}

func TestCopyDatabaseMaintenanceKeepsUnqualifiedProviderEntriesRequired(t *testing.T) {
	for _, mode := range []string{"open_database", "unowned_closed_template", "bootstrap_database"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixtureConfigured(t, func(f *fixture) {
				if mode == "open_database" {
					run(t.Context(), t, f.targetRoot, "CREATE DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()+" TEMPLATE template0")
				}
				if mode == "unowned_closed_template" {
					existingClosedTemplate(t, f)
					run(t.Context(), t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{f.template}.Sanitize()+" OWNER TO "+pgx.Identifier{f.dataOwner}.Sanitize())
				}
			})
			id := f.ordinaryOID
			if mode == "unowned_closed_template" {
				id = f.templateOID
			}
			if mode == "bootstrap_database" {
				for _, d := range f.plan.body.Source.Databases {
					if d.Name == f.bootstrap {
						id = d.OID
					}
				}
			}
			r := f.prepare(t, id)
			calls := 0
			got, e := r.WithMaintenance(t.Context(), f.target, f.exports, uuid.New(), f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
			if got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrUnsupported) || calls != 0 {
				t.Fatal("unqualified provider entry admitted", e)
			}
			assertMaintenanceOriginal(t, f, r)
			requirements, _ := f.exports.RequirementsForWorker()
			found := false
			for _, d := range requirements {
				if d.Database.OID == id {
					found = true
				}
			}
			if !found || len(requirements) != f.plan.Summary().Databases {
				t.Fatal("unqualified entry omitted from complete plan")
			}
		})
	}
}

// Drop the borrowed connection after PostgreSQL has committed the selected
// admission transaction but before the worker can return/observe its result.
// Read-only inventory transaction commits do not trigger this fault.
type maintenanceLostReplyTracer struct {
	phase, armed string
	fired        bool
}
type maintenanceCommitTraceKey struct{}

func (tr *maintenanceLostReplyTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_temp.gregale_copy_database_maintenance_change(") && len(d.Args) > 7 {
		if action, ok := d.Args[7].(string); ok {
			tr.armed = action
		}
	}
	return context.WithValue(ctx, maintenanceCommitTraceKey{}, strings.EqualFold(strings.TrimSpace(d.SQL), "commit"))
}
func (tr *maintenanceLostReplyTracer) TraceQueryEnd(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(maintenanceCommitTraceKey{}).(bool); commit && d.Err == nil && tr.armed == tr.phase && !tr.fired {
		tr.fired = true
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = c.PgConn().Close(cleanup)
	}
}
func TestCopyDatabaseMaintenanceRecoversCommittedOpeningAndClosureReplyLoss(t *testing.T) {
	for _, phase := range []string{"open", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			dispatch := uuid.New()
			cfg := f.target.Config().Copy()
			if e := f.target.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			tr := &maintenanceLostReplyTracer{phase: phase}
			cfg.Tracer = tr
			var e error
			f.target, e = pgx.ConnectConfig(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			got, e := r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
			if got != (MaintenanceClosure{}) || e == nil || !tr.fired {
				t.Fatal("lost committed reply returned successful closure", e)
			}
			allow, _, _, state := maintenanceStatus(t.Context(), t, f, f.ordinaryOID)
			if phase == "open" && (!allow || state != "open" || calls != 0) {
				t.Fatal("lost opening reply dispatched callback or lost ownership")
			}
			if phase == "finish" && (allow || state != "closed" || calls != 1) {
				t.Fatal("lost closing reply lost terminal ownership")
			}
			cfg.Tracer = nil
			f.target, e = pgx.ConnectConfig(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			original := maintenanceWindowTime(t, f, r.sourceOID)
			closure, e := r.CloseMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize)
			if e != nil || closure.ClosedAt().IsZero() || (phase == "finish" && !closure.ClosedAt().Equal(original)) {
				t.Fatal("original committed closure not recovered", e)
			}
			assertMaintenanceOriginal(t, f, r)
			got, e = r.WithMaintenance(t.Context(), f.target, f.exports, dispatch, f.authorize, func(context.Context, copyarchive.RestoreTarget) error { calls++; return nil })
			wantCalls := 0
			if phase == "finish" {
				wantCalls = 1
			}
			if got != (MaintenanceClosure{}) || !errors.Is(e, pgerrors.ErrConflict) || calls != wantCalls {
				t.Fatal("uncertain committed dispatch repeated", e)
			}
		})
	}
}
func maintenanceWindowTime(t *testing.T, f *fixture, sourceOID uint32) time.Time {
	t.Helper()
	var at *time.Time
	if e := f.target.QueryRow(t.Context(), "SELECT closed_at FROM gregale_copy_database_maintenance.windows WHERE source_oid=$1", sourceOID).Scan(&at); e != nil {
		t.Fatal(e)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}
