package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// This is a real TLS/native-publication/subscription test, including the
// sealed catalog and restarted worker. scripts/test-data-pipeline.sh provides
// its isolated logical-replication server; ordinary portable runs may skip it.
func TestNativePipelineEndToEnd(t *testing.T) {
	raw := os.Getenv("DATA_PIPELINE_TEST_DSN")
	if raw == "" {
		t.Skip("run scripts/test-data-pipeline.sh for native replication acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	sourceDB, targetDB, owner, reader := "pipeline_src_"+suffix, "pipeline_dst_"+suffix, "pipeline_owner_"+suffix, "pipeline_reader_"+suffix
	for _, name := range []string{sourceDB, targetDB} {
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+ident(name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = admin.Exec(ctx, "ALTER DATABASE "+ident(sourceDB)+" SET timezone='Europe/Istanbul'"); err != nil {
		t.Fatal(err)
	}
	sourceURL, _ := url.Parse(raw)
	sourceURL.Path = "/" + sourceDB
	targetURL, _ := url.Parse(raw)
	targetURL.Path = "/" + targetDB
	source, err := pgx.Connect(ctx, sourceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close(context.Background()) }()
	target, err := pgx.Connect(ctx, targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close(context.Background()) }()
	// Cleanup owns exactly these two databases and generated role identities.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		dst, e := pgx.Connect(cleanup, targetURL.String())
		if e == nil {
			rows, e := dst.Query(cleanup, `SELECT subname FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`)
			if e == nil {
				names := []string{}
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						names = append(names, name)
					}
				}
				rows.Close()
				for _, name := range names {
					_, _ = dst.Exec(cleanup, "ALTER SUBSCRIPTION "+ident(name)+" DISABLE")
					_, _ = dst.Exec(cleanup, "ALTER SUBSCRIPTION "+ident(name)+" SET(slot_name=NONE)")
					_, _ = dst.Exec(cleanup, "DROP SUBSCRIPTION "+ident(name))
				}
			}
			_ = dst.Close(cleanup)
		}
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+ident(targetDB)+" WITH (FORCE)")
		src, e := pgx.Connect(cleanup, sourceURL.String())
		if e == nil {
			rows, e := src.Query(cleanup, `SELECT slot_name FROM pg_replication_slots WHERE database=current_database()`)
			if e == nil {
				slots := []string{}
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						slots = append(slots, name)
					}
				}
				rows.Close()
				for _, name := range slots {
					_, _ = src.Exec(cleanup, `SELECT pg_drop_replication_slot($1)`, name)
				}
			}
			_ = src.Close(cleanup)
		}
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+ident(sourceDB)+" WITH (FORCE)")
		_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+ident(reader))
		_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+ident(owner))
	})
	if _, err = source.Exec(ctx, `CREATE TABLE public.orders(id bigint PRIMARY KEY, customer text NOT NULL, total numeric(12,2) NOT NULL, occurred_at timestamptz NOT NULL DEFAULT '2026-10-16T01:00Z'::timestamptz); INSERT INTO public.orders SELECT i,'customer-'||i,i/10.0 FROM generate_series(1,1000) i`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"CREATE ROLE " + ident(owner) + " NOLOGIN", "GRANT " + ident(owner) + " TO CURRENT_USER", "ALTER SCHEMA public OWNER TO " + ident(owner), "CREATE ROLE " + ident(reader) + " LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD 'pipeline-test'", "GRANT USAGE ON SCHEMA public TO " + ident(reader), "ALTER DEFAULT PRIVILEGES FOR ROLE " + ident(owner) + " IN SCHEMA public GRANT SELECT ON TABLES TO " + ident(reader)} {
		if _, err = target.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	s, store, identity, account := testService(t)
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "external-orders", ConnectionURL: sourceURL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `CREATE TABLE public.unsupported_table(value integer[])`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCheck(ctx, account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision}); err != nil {
		t.Fatal(err)
	}
	checkWorker := &Worker{Store: store, Identities: []*age.X25519Identity{identity}, Allowed: s.Allowed, Engine: &Engine{Policy: commit.NetworkPolicy{Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}}}
	if err = checkWorker.CheckTick(ctx); err != nil {
		t.Fatal(err)
	}
	checked, err := store.Check(ctx, account, c.ID)
	if err != nil || checked.State != "passed" || !checked.Fresh {
		t.Fatalf("native source preflight failed: %+v, %v", checked.DataConnectionCheck, err)
	}
	if len(checked.Tables) != 2 || !checked.Tables[0].Supported || checked.Tables[1].Supported {
		t.Fatalf("discovery disagreed with schema support: %+v", checked.Tables)
	}
	assertNativePreflightCompatibility(t, ctx, source, sourceURL.String(), checkWorker, account)
	t.Run("target preflight shares the empty mirror contract", func(t *testing.T) {
		probe := *checkWorker.Engine
		probe.Target = func(ctx context.Context, _, _ string, run func(*pgx.Conn, string) error) error {
			return run(target, owner)
		}
		job := ConnectionCheck{DataConnectionCheck: api.DataConnectionCheck{DatabaseID: uuid.NewString()}}
		if check := probe.Check(ctx, job, sourceURL.String()); check.State != "passed" {
			t.Fatalf("empty mirror failed compatibility: %+v", check.Checks)
		}
		if _, err := target.Exec(ctx, `CREATE TABLE public.unrelated(id integer)`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := target.Exec(ctx, `DROP TABLE public.unrelated`); err != nil {
				t.Error(err)
			}
		}()
		check := probe.Check(ctx, job, sourceURL.String())
		found := false
		for _, issue := range check.Checks {
			if issue.Code == "destination_not_empty" && issue.State == "blocked" {
				found = true
			}
		}
		if check.State != "blocked" || !found {
			t.Fatal("occupied destination passed preflight")
		}
	})
	var preflightResources int
	if err = source.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_publication)+(SELECT count(*) FROM pg_replication_slots WHERE database=current_database())`).Scan(&preflightResources); err != nil || preflightResources != 0 {
		t.Fatal("source preflight created replication resources")
	}
	databaseID := uuid.NewString()
	p, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: databaseID, Tables: []string{"orders"}, ReadSubjects: []string{"alice", "reader's\\test"}})
	if err != nil {
		t.Fatal(err)
	}
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	writerReady := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		conn, err := pgx.Connect(writerCtx, sourceURL.String())
		if err != nil {
			writerDone <- err
			close(writerReady)
			return
		}
		defer func() {
			closing, stop := context.WithTimeout(context.WithoutCancel(writerCtx), 2*time.Second)
			defer stop()
			_ = conn.Close(closing)
		}()
		if _, err = conn.Exec(writerCtx, `INSERT INTO public.orders VALUES(2000,'concurrent',0)`); err != nil {
			writerDone <- err
			close(writerReady)
			return
		}
		close(writerReady)
		for value := 1; ; value++ {
			if _, err = conn.Exec(writerCtx, `UPDATE public.orders SET total=$1 WHERE id=2000`, value); err != nil {
				if writerCtx.Err() != nil {
					err = nil
				}
				writerDone <- err
				return
			}
			select {
			case <-writerCtx.Done():
				writerDone <- nil
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	<-writerReady
	lostReply := true
	engine := &Engine{Policy: commit.NetworkPolicy{Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}, Target: func(ctx context.Context, a, id string, run func(*pgx.Conn, string) error) error {
		if a != account || id != databaseID {
			return ErrNotFound
		}
		conn, err := pgx.Connect(ctx, targetURL.String())
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err = conn.Exec(ctx, `SET timezone='UTC'`); err != nil {
			return err
		}
		if err = run(conn, owner); err != nil {
			t.Logf("native worker SQL failure: %v", err)
			return err
		}
		if lostReply {
			lostReply = false
			writerCancel()
			if err := <-writerDone; err != nil {
				t.Fatal(err)
			}
			return ErrUnavailable
		}
		return nil
	}, pinSource: func(_ context.Context, raw string) (string, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		u.Host = net.JoinHostPort(u.Hostname(), "5432")
		q := u.Query()
		q.Set("sslrootcert", os.Getenv("DATA_PIPELINE_TEST_SUBSCRIBER_CA"))
		u.RawQuery = q.Encode()
		return u.String(), nil
	}}
	worker := &Worker{Store: store, Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	tick := func() api.DataPipeline {
		t.Helper()
		if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
		if err := worker.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		item, err := store.Get(ctx, account, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return item.DataPipeline
	}
	first := tick()
	if first.State != "blocked" || lostReply {
		t.Fatalf("lost creation response was not exercised: %+v", first)
	}
	// Rebuild the worker against the same durable catalog after the lost reply.
	worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	await := func(state string) api.DataPipeline {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		var item api.DataPipeline
		for time.Now().Before(deadline) {
			item = tick()
			if item.State == state {
				return item
			}
			if item.State == "blocked" || item.State == "resync_required" {
				t.Fatalf("unexpected pipeline block: %+v", item)
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("pipeline never reached %s: %+v", state, item)
		return item
	}
	p = await("live")
	t.Run("freshness requires applied origin and survives worker restart", func(t *testing.T) {
		if _, err := target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(Pipeline{DataPipeline: p}))+" DISABLE"); err != nil {
			t.Fatal(err)
		}
		for until := time.Now().Add(10 * time.Second); ; {
			var active bool
			if err := source.QueryRow(ctx, `SELECT active FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(Pipeline{DataPipeline: p})).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if !active {
				break
			}
			if time.Now().After(until) {
				t.Fatal("subscriber did not stop")
			}
			time.Sleep(25 * time.Millisecond)
		}
		if _, err := source.Exec(ctx, `UPDATE public.orders SET customer='freshness-probe' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET progress_barrier=NULL,progress_sampled_at=NULL,applied_through_at=NULL WHERE pipeline_id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
		pool, err := engine.connectSource(ctx, sourceURL.String())
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		intent := Pipeline{DataPipeline: p}
		for i := 0; i < 2; i++ {
			ob := Observation{}
			if err := observeAppliedProgress(ctx, intent, pool, target, &ob); err != nil {
				t.Fatal(err)
			}
			if !ob.AppliedThroughAt.IsZero() {
				t.Fatal("disabled receiver acknowledged an unapplied checkpoint")
			}
		}
		if _, err := target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(intent))+" ENABLE"); err != nil {
			t.Fatal(err)
		}
		// A commit after the sampled boundary lets the origin prove the checkpoint.
		if _, err := source.Exec(ctx, `UPDATE public.orders SET customer='freshness-confirmed' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		for until := time.Now().Add(10 * time.Second); ; {
			p = tick()
			if p.AppliedThroughAt != "" {
				if p.Health == nil || p.Health.Code != "applied_progress_confirmed" {
					t.Fatalf("untrusted freshness: %+v", p)
				}
				break
			}
			if time.Now().After(until) {
				t.Fatal("applied checkpoint was never confirmed")
			}
			time.Sleep(50 * time.Millisecond)
		}
		restarted := NewPostgresStore(store.pool)
		saved, err := restarted.Get(ctx, account, p.ID)
		if err != nil || saved.AppliedThroughAt != p.AppliedThroughAt {
			t.Fatalf("lost durable applied evidence: %v", err)
		}
	})

	var finalConcurrent string
	if err = source.QueryRow(ctx, `SELECT total::text FROM public.orders WHERE id=2000`).Scan(&finalConcurrent); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(10 * time.Second); ; {
		var current string
		err = target.QueryRow(ctx, `SELECT total::text FROM public.orders WHERE id=2000`).Scan(&current)
		if err == nil && current == finalConcurrent {
			break
		}
		if time.Now().After(until) {
			t.Fatal("concurrent copy/update boundary lost the final source value")
		}
		time.Sleep(25 * time.Millisecond)
	}
	var count int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 1001 {
		t.Fatalf("initial copy: count=%d error=%v", count, err)
	}
	if _, err = source.Exec(ctx, `INSERT INTO public.orders VALUES(1001,'new',9.99); UPDATE public.orders SET total=77.77 WHERE id=1; DELETE FROM public.orders WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	assertRows := func() {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var correct bool
			err := target.QueryRow(ctx, `SELECT count(*)=1001 AND bool_or(id=1 AND total=77.77) AND bool_or(id=1001) AND NOT bool_or(id=2) FROM public.orders`).Scan(&correct)
			if err == nil && correct {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("insert/update/delete did not converge")
	}
	assertRows()
	assertNativeDataAPI(t, ctx, targetURL.String(), reader)
	if _, err = target.Exec(ctx, "SET ROLE "+ident(reader)); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"bob"}',false)`); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign JWT subject read mirror: %d %v", count, err)
	}
	if _, err = target.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"alice"}',false)`); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 1001 {
		t.Fatalf("allowed subject cannot read: %d %v", count, err)
	}
	claims, _ := json.Marshal(map[string]string{"sub": "reader's\\test"})
	if _, err = target.Exec(ctx, `SELECT set_config('request.jwt.claims',$1,false)`, string(claims)); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 1001 {
		t.Fatal("quoted subject was not safely authorized")
	}
	if _, err = target.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"alice"}',false)`); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, `DELETE FROM public.orders`); err == nil {
		t.Fatal("read-only app role modified replicated rows")
	}
	if _, err = target.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	// Explicit bounded verification must distinguish proven equality from
	// an observation that still needs another replication acknowledgement.
	verification, err := store.Control(ctx, account, p.ID, p.Revision, "verify")
	if err != nil {
		t.Fatal(err)
	}
	p = verification.DataPipeline
	for until := time.Now().Add(10 * time.Second); ; {
		p = tick()
		if p.Verification == "passed" {
			break
		}
		if p.Verification == "inconclusive" {
			next, e := store.Control(ctx, account, p.ID, p.Revision, "verify")
			if e != nil {
				t.Fatal(e)
			}
			p = next.DataPipeline
		}
		if time.Now().After(until) {
			t.Fatalf("idle source did not verify: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	paused, err := store.Control(ctx, account, p.ID, p.Revision, "pause")
	if err != nil {
		t.Fatal(err)
	}
	p = paused.DataPipeline
	p = await("paused")
	var enabled bool
	if err = target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(Pipeline{DataPipeline: p})).Scan(&enabled); err != nil || enabled {
		t.Fatal("paused subscription still enabled")
	}
	resumed, err := store.Control(ctx, account, p.ID, p.Revision, "resume")
	if err != nil {
		t.Fatal(err)
	}
	p = resumed.DataPipeline
	p = await("live")
	p = assertNativeHealthAlerts(t, ctx, s, store, engine, identity, sourceURL.String(), p, await)
	p = assertNativeLargeVerification(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	p = assertNativeSchemaEvolution(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, p)
	p = assertNativeSchemaBackfill(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	p = assertNativeTableAddition(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	p = assertNativeTableCancellation(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	p = assertNativeLiveTableRepair(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	p = assertNativeLiveTableRemoval(t, ctx, store, engine, source, target, targetURL.String(), reader, account, tick, func() {
		worker = &Worker{Store: NewPostgresStore(store.pool), Identities: []*age.X25519Identity{identity}, Engine: engine, Allowed: func(context.Context, string) bool { return true }}
	}, p)
	// Unsupported schema changes still require an explicit copy.
	if _, err = source.Exec(ctx, `ALTER TABLE public.orders RENAME COLUMN historical TO renamed_historical`); err != nil {
		t.Fatal(err)
	}
	blocked := tick()
	if blocked.State != "resync_required" || blocked.Reason != "source_schema_changed" {
		t.Fatalf("schema drift did not block: %+v", blocked)
	}
	resync, err := store.Control(ctx, account, p.ID, p.Revision, "resync")
	if err != nil {
		t.Fatal(err)
	}
	p = resync.DataPipeline
	p = await("live")
	assertRows()
	// Default SELECT grants survive recreation because tables are created by
	// the stable schema owner, not merely reassigned to that owner afterward.
	if _, err = target.Exec(ctx, "SET ROLE "+ident(reader)); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 1001 {
		t.Fatalf("resync lost read grants: %v", err)
	}
	_, _ = target.Exec(ctx, `RESET ROLE`)
	// A lost upstream slot must not be silently replaced with a gap in data.
	name := pipelineName(Pipeline{DataPipeline: p})
	if err = setSubscriptionEnabled(ctx, Pipeline{DataPipeline: p}, target, false); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(5 * time.Second); ; {
		_, err = source.Exec(ctx, `SELECT pg_drop_replication_slot($1)`, name)
		if err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	lost := tick()
	if lost.State != "resync_required" || lost.Reason != "replication_state_lost" {
		t.Fatalf("lost WAL state accepted: %+v", lost)
	}
	// Simulate losing the catalog acknowledgement after remote retirement.
	// The durable target receipt must prevent a cleanup-only slot or a new
	// subscription from being used to resume this broken generation.
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET state='live',reason='',retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	lost = tick()
	if lost.State != "resync_required" {
		t.Fatalf("retired generation resumed after lost acknowledgement: %+v", lost)
	}
	var activeSubscriptions int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM pg_subscription WHERE subname=$1 AND subenabled`, name).Scan(&activeSubscriptions); err != nil || activeSubscriptions != 0 {
		t.Fatal("retired generation re-enabled")
	}
	deleted, err := store.Control(ctx, account, p.ID, p.Revision, "delete")
	if err != nil {
		t.Fatal(err)
	}
	p = deleted.DataPipeline
	p = await("deleted")
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count); err != nil || count != 1001 {
		t.Fatal("pipeline deletion removed customer mirror data")
	}
	if err = source.QueryRow(ctx, `SELECT count(*) FROM pg_publication WHERE pubname LIKE 'gregale_sync_%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("publication leaked after deletion")
	}
	if err = store.DeleteConnection(ctx, account, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Connection(ctx, account, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("credential not deleted after cleanup")
	}
}

func assertNativePreflightCompatibility(t *testing.T, ctx context.Context, source *pgx.Conn, raw string, worker *Worker, account string) {
	t.Helper()
	engine := worker.Engine
	t.Run("limited source role receives actionable blockers", func(t *testing.T) {
		role := "pipeline_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := source.Exec(ctx, `CREATE ROLE `+ident(role)+` LOGIN PASSWORD 'probe-secret'`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := source.Exec(ctx, `DROP OWNED BY `+ident(role)); err != nil {
				t.Error(err)
			}
			if _, err := source.Exec(ctx, `DROP ROLE `+ident(role)); err != nil {
				t.Error(err)
			}
		}()
		if _, err := source.Exec(ctx, `GRANT USAGE ON SCHEMA public TO `+ident(role)); err != nil {
			t.Fatal(err)
		}
		if _, err := source.Exec(ctx, `GRANT SELECT ON public.orders TO `+ident(role)); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(role, "probe-secret")
		result := engine.Check(ctx, ConnectionCheck{}, u.String())
		if result.State != "blocked" {
			t.Fatal("underprivileged source passed preflight")
		}
		found := false
		for _, check := range result.Checks {
			if check.Code == "source_replication_permission_required" && check.State == "blocked" && check.Remediation != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("replication permission blocker missing: %+v", result.Checks)
		}
	})
	t.Run("initial copy slot demand blocks admission", func(t *testing.T) {
		var available int
		if err := source.QueryRow(ctx, `SELECT current_setting('max_replication_slots')::integer-(SELECT count(*)::integer FROM pg_replication_slots)`).Scan(&available); err != nil {
			t.Fatal(err)
		}
		if available != 20 {
			t.Fatal("native capacity test requires its dedicated 20-slot fixture")
		}
		slots := []string{}
		defer func() {
			for _, slot := range slots {
				if _, err := source.Exec(ctx, `SELECT pg_drop_replication_slot($1)`, slot); err != nil {
					t.Error(err)
				}
			}
		}()
		for range 18 {
			name := "pipeline_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if _, err := source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, name); err != nil {
				t.Fatal(err)
			}
			slots = append(slots, name)
		}
		result := engine.Check(ctx, ConnectionCheck{}, raw)
		if result.State != "blocked" {
			t.Fatal("insufficient source slot capacity passed preflight")
		}
		found := false
		for _, check := range result.Checks {
			if check.Code == "source_replication_slots_capacity" && check.State == "blocked" {
				found = true
			}
		}
		if !found {
			t.Fatalf("slot capacity blocker missing: %+v", result.Checks)
		}
	})
	t.Run("wide schemas finish within the catalog result budget", func(t *testing.T) {
		columns := []string{"id integer PRIMARY KEY"}
		for n := range api.DataPipelineColumnsMax - 1 {
			columns = append(columns, fmt.Sprintf("column_%03d integer[]", n))
		}
		create, drop := []string{}, []string{}
		for n := range api.DataPipelineDiscoveryTablesMax {
			name := fmt.Sprintf("metadata_%03d", n)
			create = append(create, "CREATE TABLE "+tableIdent(name)+" ("+strings.Join(columns, ",")+")")
			drop = append(drop, "DROP TABLE IF EXISTS "+tableIdent(name))
		}
		defer func() {
			if _, err := source.Exec(ctx, strings.Join(drop, ";")); err != nil {
				t.Error(err)
			}
		}()
		if _, err := source.Exec(ctx, strings.Join(create, ";")); err != nil {
			t.Fatal(err)
		}
		store, ok := worker.Store.(*PostgresStore)
		if !ok {
			t.Fatal("native preflight requires its PostgreSQL catalog fixture")
		}
		s := &Service{Store: store, Recipient: worker.Identities[0].Recipient(), Allowed: worker.Allowed}
		c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "wide-source", ConnectionURL: raw})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RequestCheck(ctx, account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision}); err != nil {
			t.Fatal(err)
		}
		if err = worker.CheckTick(ctx); err != nil {
			t.Fatalf("large metadata failed the worker: %v", err)
		}
		check, err := store.Check(ctx, account, c.ID)
		if err != nil || check.State != "passed" || !check.Truncated || len(check.Tables) >= api.DataPipelineDiscoveryTablesMax {
			t.Fatalf("unbounded discovery result: tables=%d state=%s truncated=%v err=%v", len(check.Tables), check.State, check.Truncated, err)
		}
		var size int
		if err = store.pool.QueryRow(ctx, `SELECT octet_length(result::text) FROM data_connection_checks WHERE connection_id=$1`, c.ID).Scan(&size); err != nil || size > api.DataPipelineCheckMaxBytes {
			t.Fatal("persisted metadata exceeded its catalog limit")
		}
	})
}
