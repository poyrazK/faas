package datapipeline

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
)

type nativeRecoveryFixture struct {
	admin, source, target *pgx.Conn
	service               *Service
	store                 *PostgresStore
	engine                *Engine
	worker                *Worker
	connection            api.DataConnection
	pipeline              Pipeline
	raw, role             string
}

func newNativeRecoveryFixture(t *testing.T, initialSQL string) *nativeRecoveryFixture {
	t.Helper()
	raw := os.Getenv("DATA_PIPELINE_TEST_DSN")
	if raw == "" {
		t.Skip("run scripts/test-data-pipeline.sh for native replication acceptance")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	sourceDB, targetDB := "recovery_src_"+suffix, "recovery_dst_"+suffix
	role, owner := "recovery_source_"+suffix, "recovery_owner_"+suffix
	f := &nativeRecoveryFixture{admin: admin, role: role}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if f.target != nil {
			rows, err := f.target.Query(cleanup, `SELECT subname FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`)
			var names []string
			if err == nil {
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						names = append(names, name)
					}
				}
				rows.Close()
			}
			for _, name := range names {
				_, _ = f.target.Exec(cleanup, "ALTER SUBSCRIPTION "+ident(name)+" DISABLE")
				_, _ = f.target.Exec(cleanup, "ALTER SUBSCRIPTION "+ident(name)+" SET(slot_name=NONE)")
				_, _ = f.target.Exec(cleanup, "DROP SUBSCRIPTION "+ident(name))
			}
			_ = f.target.Close(cleanup)
		}
		if f.source != nil {
			rows, err := f.source.Query(cleanup, `SELECT slot_name FROM pg_replication_slots WHERE database=current_database()`)
			var slots []string
			if err == nil {
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						slots = append(slots, name)
					}
				}
				rows.Close()
			}
			for _, name := range slots {
				_, _ = f.source.Exec(cleanup, `SELECT pg_drop_replication_slot($1)`, name)
			}
			_ = f.source.Close(cleanup)
		}
		_, _ = admin.Exec(cleanup, "DROP DATABASE IF EXISTS "+ident(targetDB)+" WITH (FORCE)")
		_, _ = admin.Exec(cleanup, "DROP DATABASE IF EXISTS "+ident(sourceDB)+" WITH (FORCE)")
		_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+ident(role))
		_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+ident(owner))
		_ = admin.Close(cleanup)
	})
	for _, sql := range []string{"CREATE DATABASE " + ident(sourceDB), "CREATE DATABASE " + ident(targetDB), "CREATE ROLE " + ident(role) + " LOGIN SUPERUSER PASSWORD 'before' IN ROLE pipeline_password_test", "CREATE ROLE " + ident(owner) + " NOLOGIN"} {
		if _, err = admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	sourceURL, _ := url.Parse(raw)
	sourceURL.Path, sourceURL.User = "/"+sourceDB, url.UserPassword(role, "before")
	f.raw = sourceURL.String()
	f.source, err = pgx.Connect(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	targetURL, _ := url.Parse(raw)
	targetURL.Path = "/" + targetDB
	f.target, err = pgx.Connect(ctx, targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.target.Exec(ctx, "ALTER SCHEMA public OWNER TO "+ident(owner)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.source.Exec(ctx, initialSQL); err != nil {
		t.Fatal(err)
	}
	var identity *age.X25519Identity
	var account string
	f.service, f.store, identity, account = testService(t)
	f.connection, err = f.service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "recovery-source", ConnectionURL: f.raw})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, f.store, account, f.connection)
	created, err := f.service.Create(ctx, account, api.CreateDataPipelineRequest{Name: "recovery-orders", SourceConnectionID: f.connection.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	f.pipeline, err = f.store.Get(ctx, account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = &Engine{
		Policy: commit.NetworkPolicy{Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
		Target: func(ctx context.Context, _, _ string, run func(*pgx.Conn, string) error) error {
			return run(f.target, owner)
		},
		pinSource: func(_ context.Context, raw string) (string, error) {
			u, err := url.Parse(raw)
			if err != nil {
				return "", err
			}
			u.Host = net.JoinHostPort(u.Hostname(), "5432")
			q := u.Query()
			q.Set("sslrootcert", os.Getenv("DATA_PIPELINE_TEST_SUBSCRIBER_CA"))
			u.RawQuery = q.Encode()
			return u.String(), nil
		},
	}
	f.worker = &Worker{Store: f.store, Engine: f.engine, Identities: []*age.X25519Identity{identity}, Allowed: f.service.Allowed}
	f.await(t, "live")
	return f
}

func (f *nativeRecoveryFixture) tick(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if _, err := f.store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, f.pipeline.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var err error
	f.pipeline, err = f.store.Get(ctx, f.pipeline.AccountID, f.pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *nativeRecoveryFixture) control(t *testing.T, action string) {
	t.Helper()
	p, err := f.store.Control(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, action)
	if err != nil {
		t.Fatal(err)
	}
	f.pipeline = p
}

func (f *nativeRecoveryFixture) await(t *testing.T, state string) {
	t.Helper()
	for until := time.Now().Add(20 * time.Second); ; {
		f.tick(t)
		if f.pipeline.State == state {
			return
		}
		if f.pipeline.State == "blocked" || f.pipeline.State == "resync_required" || time.Now().After(until) {
			t.Fatalf("pipeline did not reach %s: %+v", state, f.pipeline.DataPipeline)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *nativeRecoveryFixture) assertRetired(t *testing.T, p Pipeline) {
	t.Helper()
	ctx := t.Context()
	var subscriptions, publications, slots int
	if err := f.target.QueryRow(ctx, `SELECT count(*) FROM pg_subscription WHERE subname=$1`, pipelineName(p)).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if err := f.source.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_publication WHERE pubname=$1),(SELECT count(*) FROM pg_replication_slots WHERE slot_name=$1)`, pipelineName(p)).Scan(&publications, &slots); err != nil {
		t.Fatal(err)
	}
	if subscriptions != 0 || publications != 0 || slots != 0 {
		t.Fatalf("retired generation still owns native resources: subscriptions=%d publications=%d slots=%d", subscriptions, publications, slots)
	}
}

func TestNativePipelineRepeatedResyncRecovery(t *testing.T) {
	for _, action := range []string{"resync", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
			original := f.pipeline
			f.control(t, "resync")
			target := f.engine.Target
			f.engine.Target = func(context.Context, string, string, func(*pgx.Conn, string) error) error { return ErrUnavailable }
			f.tick(t)
			f.engine.Target = target
			if f.pipeline.State != "blocked" {
				t.Fatal("destination outage was not recorded")
			}
			f.control(t, "resync")
			if action == "delete" {
				f.control(t, "delete")
				f.await(t, "deleted")
			} else {
				f.await(t, "live")
			}
			f.assertRetired(t, original)
			var retained bool
			if err := f.target.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM public.orders WHERE id=1 AND value='retained')`).Scan(&retained); err != nil || !retained {
				t.Fatal("recovery lost imported data", retained, err)
			}
		})
	}
}

func TestNativePipelineCleanupAfterCredentialRotation(t *testing.T) {
	for _, action := range []string{"resync", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
			original := f.pipeline
			if _, err := f.admin.Exec(t.Context(), "ALTER ROLE "+ident(f.role)+" PASSWORD 'after'"); err != nil {
				t.Fatal(err)
			}
			if stale, err := pgx.Connect(t.Context(), f.raw); err == nil {
				_ = stale.Close(t.Context())
				t.Fatal("credential rotation test requires password authentication")
			}
			u, _ := url.Parse(f.raw)
			u.User = url.UserPassword(f.role, "after")
			if _, err := f.service.RotateConnection(t.Context(), f.pipeline.AccountID, f.connection.ID, api.RotateDataConnectionRequest{ExpectedRevision: f.connection.Revision, ConnectionURL: u.String()}); err != nil {
				t.Fatal(err)
			}
			// No ordinary running reconciliation may refresh the subscription
			// between rotation and this cleanup request.
			f.control(t, action)
			state := "live"
			if action == "delete" {
				state = "deleted"
			}
			f.await(t, state)
			f.assertRetired(t, original)
		})
	}
}

func TestNativePipelineIncludedPrimaryKeyColumns(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer,value text,PRIMARY KEY(id) INCLUDE(value));INSERT INTO public.orders VALUES(1,NULL),(2,'before')`)
	ctx := t.Context()
	if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='after' WHERE id=1;UPDATE public.orders SET id=3 WHERE id=2;DELETE FROM public.orders WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(10 * time.Second); ; {
		var matches bool
		if err := f.target.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(id=1 AND value='after') FROM public.orders`).Scan(&matches); err != nil {
			t.Fatal(err)
		}
		if matches {
			return
		}
		if time.Now().After(until) {
			t.Fatal("INCLUDE column prevented initial copy or continuing replication")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRetiredGenerationNames(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 3}}
	previous := p
	previous.Generation = 1
	for _, test := range []struct {
		name string
		want bool
	}{
		{pipelineName(previous), true},
		{pipelineName(p), false},
		{strings.TrimSuffix(pipelineName(previous), "1") + "01", false},
		{strings.TrimSuffix(pipelineName(previous), "1") + "0", false},
		{strings.TrimSuffix(pipelineName(previous), "1") + "-1", false},
		{"unrelated_1", false},
	} {
		_, ok := retiredGeneration(p, test.name)
		if ok != test.want {
			t.Errorf("generation identity %q: got %v want %v", test.name, ok, test.want)
		}
	}
}

func TestNativeRetiredGenerationOwnershipConflict(t *testing.T) {
	for _, object := range []string{"PUBLICATION", "SUBSCRIPTION"} {
		t.Run(object, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text)`)
			original := f.pipeline
			conn, reason := f.source, "source_publication_conflict"
			if object == "SUBSCRIPTION" {
				conn, reason = f.target, "destination_subscription_conflict"
			}
			if _, err := conn.Exec(t.Context(), "COMMENT ON "+object+" "+ident(pipelineName(original))+" IS 'unrelated'"); err != nil {
				t.Fatal(err)
			}
			f.control(t, "resync")
			f.control(t, "pause")
			f.tick(t)
			if f.pipeline.Reason != reason {
				t.Fatalf("foreign ownership was adopted: %+v", f.pipeline.DataPipeline)
			}
			// Ownership rejection must precede any retired-generation teardown.
			var enabled bool
			if err := f.target.QueryRow(t.Context(), `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(original)).Scan(&enabled); err != nil || !enabled {
				t.Fatal("ownership conflict changed the old subscription", enabled, err)
			}
			if _, err := conn.Exec(t.Context(), "COMMENT ON "+object+" "+ident(pipelineName(original))+" IS "+literal(marker(original))); err != nil {
				t.Fatal(err)
			}
			f.control(t, "delete")
			f.await(t, "deleted")
			f.assertRetired(t, original)
		})
	}
}

func TestNativePipelineRetiresSubscriptionWithoutMirrorReceipt(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	original := f.pipeline
	// A subscription's shared ownership comment remains an independent
	// recovery anchor when the target receipt and publication were lost.
	if _, err := f.target.Exec(t.Context(), `DELETE FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, f.pipeline.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(t.Context(), "DROP PUBLICATION "+ident(pipelineName(original))); err != nil {
		t.Fatal(err)
	}
	f.control(t, "resync")
	target := f.engine.Target
	f.engine.Target = nil
	f.tick(t)
	f.engine.Target = target
	f.control(t, "resync")
	f.await(t, "live")
	f.assertRetired(t, original)
}
