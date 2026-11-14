package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestScheduledWorkValidationAndUTCWindows(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{Operations: &api.DataPipelineOperations{Version: 1, Workload: &api.DataPipelineWorkload{Window: &api.DataPipelineWorkWindow{StartMinuteUTC: 23 * 60, EndMinuteUTC: 60}}}}}
	for _, tc := range []struct {
		hour    int
		allowed bool
	}{{0, true}, {1, false}, {22, false}, {23, true}} {
		at := time.Date(2026, 11, 14, tc.hour, 0, 0, 0, time.UTC)
		if workAllowed(p, at) != tc.allowed || !workAllowed(p, nextWorkTime(p, at)) {
			t.Fatal("cross-midnight window", tc)
		}
	}
	base := &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: 3600}}
	if !validOperations(normalizedOperations(base), []string{"orders"}) {
		t.Fatal("default snapshot budgets rejected")
	}
	for _, rule := range []api.DataPipelineQualityRule{
		{Name: "unsafe", Kind: "sql", Table: "orders", Columns: []string{"id"}},
		{Name: "range", Kind: "range", Table: "orders", Columns: []string{"total"}, Minimum: "1e100"},
		{Name: "range", Kind: "range", Table: "orders", Columns: []string{"total"}, Minimum: "2.000000000000000001", Maximum: "2"},
		{Name: "required", Kind: "required", Table: "orders", Columns: []string{""}},
	} {
		c := &api.DataPipelineOperations{Version: 1, Quality: &api.DataPipelineQualitySchedule{EverySeconds: 3600, Rules: []api.DataPipelineQualityRule{rule}}}
		if validOperations(c, []string{"orders"}) {
			t.Fatal("unsafe rule accepted", rule.Name)
		}
	}
}

func TestScheduledWorkQueryBudgetAndVerificationDeferral(t *testing.T) {
	db := pgtest.OpenDatabase(t)
	p := Pipeline{DataPipeline: api.DataPipeline{Operations: &api.DataPipelineOperations{Version: 1, Workload: &api.DataPipelineWorkload{QueryTimeoutMillis: 50, BatchesPerTick: 1}}}}
	ctx := workloadContext(t.Context(), p)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err = verificationSetting(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_sleep(0.2)`); err == nil {
		t.Fatal("query exceeded configured server deadline")
	}
	now := time.Now().UTC()
	minute := now.Hour()*60 + now.Minute()
	p.Operations.Workload.Window = &api.DataPipelineWorkWindow{StartMinuteUTC: (minute + 2) % 1440, EndMinuteUTC: (minute + 3) % 1440}
	var ob Observation
	done, err := verifyResumable(ctx, p, nil, nil, nil, &ob, func(context.Context) error { t.Error("deferred work reached endpoint fence"); return nil })
	if err != nil || done || ob.Reason != "maintenance_window" || workBatches(p) != 1 {
		t.Fatal("verification ignored work policy", ob.Reason, err)
	}
}

func TestScheduledWorkersFenceOlderClaims(t *testing.T) {
	s, store, _, account := testService(t)
	c, err := s.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCheck(t.Context(), account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, Mode: "snapshot"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.q.PipelineCheckClaim(t.Context(), store.pool, "older-check-worker"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old worker claimed snapshot check", err)
	}
	job, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCheck(t.Context(), job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: "scheduled", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}, Operations: &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: 3600}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.q.PipelineClaim(t.Context(), store.pool, "older-pipeline-worker"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old worker claimed scheduled pipeline", err)
	}
	claimed, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil || claimed.ID != p.ID {
		t.Fatal("capable worker could not claim", err)
	}
}

// Real PostgreSQL endpoints, a SELECT-only source login, and the real catalog
// worker. Only dialing is substituted: production retains its verified TLS seam.
func scheduledFixture(t *testing.T) (*Service, *PostgresStore, *Worker, *pgxpool.Pool, *pgxpool.Pool, Pipeline) {
	t.Helper()
	s, store, key, account := testService(t)
	role := "scheduled_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := store.pool.Exec(t.Context(), "CREATE ROLE "+ident(role)+" LOGIN NOSUPERUSER NOREPLICATION NOBYPASSRLS PASSWORD 'test-readonly'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.pool.Exec(context.Background(), "DROP ROLE "+ident(role)) })
	source, target := pgtest.OpenDatabase(t), pgtest.OpenDatabase(t)
	if _, err := source.Exec(t.Context(), `CREATE TABLE orders(id bigint PRIMARY KEY,tenant_id text NOT NULL,total numeric,payload json,private_note text); INSERT INTO orders VALUES(1,'a',9007199254740993.123456789,'{"n":9007199254740993.123456789}', 'private-row-sentinel'),(2,'b',-1,'{"ok":true}', 'private-row-sentinel'); ALTER TABLE orders REPLICA IDENTITY NOTHING`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"GRANT CONNECT ON DATABASE " + ident(source.Config().ConnConfig.Database) + " TO " + ident(role), "GRANT USAGE ON SCHEMA public TO " + ident(role), "GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + ident(role), "ALTER ROLE " + ident(role) + " SET default_transaction_read_only=on"} {
		if _, err := source.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	cfg := source.Config().Copy()
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = "test-readonly"
	engine := &Engine{openSource: func(ctx context.Context, _ string) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, cfg.Copy())
	}, Target: func(ctx context.Context, _, _ string, run func(*pgx.Conn, string) error) error {
		c, err := target.Acquire(ctx)
		if err != nil {
			return err
		}
		defer c.Release()
		return run(c.Conn(), target.Config().ConnConfig.User)
	}}
	registeredURL := "postgres://reader:sealed-password@db.example/db?sslmode=verify-full"
	if raw := os.Getenv("DATA_PIPELINE_TEST_DSN"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + source.Config().ConnConfig.Database
		u.User = url.UserPassword(role, "test-readonly")
		registeredURL = u.String()
		engine.openSource = nil
		engine.Policy = commit.NetworkPolicy{Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	}
	c, err := s.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "readonly", ConnectionURL: registeredURL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCheck(t.Context(), account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, Mode: "snapshot"}); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	checked := engine.Check(t.Context(), job, registeredURL)
	if len(checked.Tables) != 1 || !checked.Tables[0].Supported {
		t.Fatalf("SELECT-only source with nonlogical WAL/identity was rejected: %+v", checked)
	}
	// Physical placement qualification is independently tested by capacity tests.
	checked.State = "passed"
	checked.Capacity = nil
	if err = store.FinishCheck(t.Context(), job, checked); err != nil {
		t.Fatal(err)
	}
	created, err := s.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: "scheduled", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, Serving: &api.DataPipelineServing{Version: 1, Columns: map[string][]string{"orders": {"id", "tenant_id", "total", "payload"}}, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", Memberships: []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}, {Subject: "bob", Tenant: "b"}}}}, Operations: &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: 3600}, Quality: &api.DataPipelineQualitySchedule{EverySeconds: 3600, Rules: []api.DataPipelineQualityRule{{Name: "positive_total", Kind: "range", Table: "orders", Columns: []string{"total"}, Minimum: "0"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Get(t.Context(), account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{Store: store, Engine: engine, Identities: []*age.X25519Identity{key}, Allowed: s.Allowed}
	return s, store, w, source, target, p
}

func scheduledTick(t *testing.T, store *PostgresStore, w *Worker, p Pipeline) Pipeline {
	t.Helper()
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	next, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestScheduledSnapshotRetainsLastGoodCaptureAndRecoversAcknowledgement(t *testing.T) {
	s, store, w, source, target, p := scheduledFixture(t)
	p = scheduledTick(t, store, w, p)
	if p.State != "live" || p.OperationsStatus == nil || p.OperationsStatus.Snapshot.State != "succeeded" {
		t.Fatalf("first capture: %+v", p)
	}
	if p.OperationsStatus.Quality == nil || p.OperationsStatus.Quality.State != "failed" || p.OperationsStatus.Quality.Results[0].Violations != 1 {
		t.Fatalf("quality: %+v", p.OperationsStatus)
	}
	var precise, payload string
	if err := target.QueryRow(t.Context(), `SELECT total::text,payload::text FROM orders WHERE id=1`).Scan(&precise, &payload); err != nil || precise != "9007199254740993.123456789" || !strings.Contains(payload, "9007199254740993.123456789") {
		t.Fatal("precision lost", precise, payload, err)
	}
	var excluded bool
	if err := target.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='orders'::regclass AND attname='private_note')`).Scan(&excluded); err != nil || excluded {
		t.Fatal("excluded data copied", err)
	}
	var native int
	if err := source.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM pg_publication)+(SELECT count(*) FROM pg_replication_slots WHERE database=current_database())`).Scan(&native); err != nil || native != 0 {
		t.Fatal("snapshot created replication resources", err)
	}
	// Publishing a new capture must preserve old repeatable-read snapshots,
	// public relation identities and the tenant policies attached to them.
	old, err := target.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Rollback(t.Context()) }()
	var beforeOID, afterOID uint32
	if err = old.QueryRow(t.Context(), `SELECT 'orders'::regclass::oid,total::text FROM orders WHERE id=1`).Scan(&beforeOID, &precise); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(t.Context(), `UPDATE orders SET total=20 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_snapshot"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Snapshot.State != "succeeded" {
		t.Fatal("manual refresh", p.OperationsStatus.Snapshot)
	}
	if err = old.QueryRow(t.Context(), `SELECT total::text FROM orders WHERE id=1`).Scan(&precise); err != nil || precise != "9007199254740993.123456789" {
		t.Fatal("publication destroyed old SQL snapshot", precise, err)
	}
	if err = target.QueryRow(t.Context(), `SELECT 'orders'::regclass::oid,total::text FROM orders WHERE id=1`).Scan(&afterOID, &precise); err != nil || precise != "20" || afterOID != beforeOID {
		t.Fatal("refresh replaced public identity or failed to publish", precise, err)
	}
	_ = old.Rollback(t.Context())
	reader := "scheduled_reader_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = target.Exec(t.Context(), "CREATE ROLE "+ident(reader)+" NOLOGIN NOINHERIT; GRANT USAGE ON SCHEMA public TO "+ident(reader)+"; GRANT SELECT ON orders TO "+ident(reader)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = target.Exec(context.Background(), "DROP OWNED BY "+ident(reader)+"; DROP ROLE "+ident(reader))
	})
	read, err := target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = read.Exec(t.Context(), "SET LOCAL ROLE "+ident(reader)+`; SELECT set_config('request.jwt.claims','{"sub":"alice","tenant":"b"}',true)`); err != nil {
		t.Fatal(err)
	}
	var visible []int64
	if err = read.QueryRow(t.Context(), `SELECT array_agg(id ORDER BY id) FROM orders`).Scan(&visible); err != nil || len(visible) != 1 || visible[0] != 1 {
		t.Fatal("snapshot crossed tenant boundary", visible, err)
	}
	_ = read.Rollback(t.Context())
	if err := store.RequestTables(t.Context(), p.AccountID, p.ID, p.Revision, []string{"other"}); !errors.Is(err, ErrConflict) {
		t.Fatal("snapshot entered CDC evolution", err)
	}
	if err := s.Operations(t.Context(), uuid.NewString(), p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_snapshot"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign operation", err)
	}
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision + 1, Action: "run_snapshot"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale operation", err)
	}
	captured := p.OperationsStatus.Snapshot.CapturedAt
	open := w.Engine.openSource
	w.Engine.openSource = func(context.Context, string) (*pgxpool.Pool, error) {
		t.Error("quality configuration woke an idle source")
		return nil, ErrUnavailable
	}
	config := normalizedOperations(p.Operations)
	config.Quality = nil
	if err = s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "configure", Config: config}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Snapshot.State != "succeeded" || p.OperationsStatus.Snapshot.CapturedAt != captured {
		t.Fatal("configuration recaptured source", p.OperationsStatus.Snapshot)
	}
	// Simulate lost catalog acknowledgement after the atomic target commit.
	status := operationsStatus(p)
	status.Snapshot.State = "running"
	status.Snapshot.CapturedAt = ""
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET work_status=$2 WHERE id=$1`, p.ID, operationsJSON(status)); err != nil {
		t.Fatal(err)
	}
	w.Engine.openSource = func(context.Context, string) (*pgxpool.Pool, error) {
		t.Error("committed receipt recovery opened source")
		return nil, ErrUnavailable
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Snapshot.State != "succeeded" || p.OperationsStatus.Snapshot.CapturedAt != captured {
		t.Fatal("receipt did not recover original capture", p.OperationsStatus)
	}
	// A genuinely new run fails; the previous readable capture stays intact.
	w.Engine.openSource = func(context.Context, string) (*pgxpool.Pool, error) { return nil, ErrUnavailable }
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_snapshot"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.State != "live" || p.OperationsStatus.Snapshot.State != "failed" || p.OperationsStatus.Snapshot.CapturedAt != captured || !snapshotStale(p.DataPipeline, time.Now()) {
		t.Fatal("failed refresh lost capture", p.OperationsStatus)
	}
	var count int
	if err := target.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&count); err != nil || count != 2 {
		t.Fatal("last good rows changed", count, err)
	}
	w.Engine.openSource = open
	// A budget failure also rolls back all staged data.
	config = normalizedOperations(p.Operations)
	config.Snapshot.MaxRows = 1
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "configure", Config: config}); err != nil {
		t.Fatal(err)
	}
	p, err = store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_snapshot"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Snapshot.Reason != "snapshot_budget_exceeded" || p.OperationsStatus.Snapshot.CapturedAt != captured {
		t.Fatal("budget failure", p.OperationsStatus)
	}
	// Pause and disconnect remain available outside all work windows.
	now := time.Now().UTC()
	minute := now.Hour()*60 + now.Minute()
	config.Workload = &api.DataPipelineWorkload{Window: &api.DataPipelineWorkWindow{StartMinuteUTC: (minute + 2) % 1440, EndMinuteUTC: (minute + 3) % 1440}, QueryTimeoutMillis: 50, BatchesPerTick: 1}
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "configure", Config: config}); err != nil {
		t.Fatal(err)
	}
	p, err = store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_snapshot"}); err != nil {
		t.Fatal(err)
	}
	w.Engine.openSource = func(context.Context, string) (*pgxpool.Pool, error) {
		t.Error("work window opened source")
		return nil, ErrUnavailable
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Snapshot.State != "due" || !snapshotStale(p.DataPipeline, time.Now()) {
		t.Fatal("queued refresh cleared failed capture", p.OperationsStatus.Snapshot)
	}
	if _, err := s.Control(t.Context(), p.AccountID, p.ID, api.ControlDataPipelineRequest{ExpectedRevision: p.Revision, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	targetAccess := w.Engine.Target
	w.Engine.Target = func(ctx context.Context, account, id string, run func(*pgx.Conn, string) error) error {
		if workQueryMillis(ctx) != api.DataPipelineVerificationQueryTimeout.Milliseconds() {
			t.Error("optional work budget reduced lifecycle-control deadlines")
		}
		return targetAccess(ctx, account, id, run)
	}
	p = scheduledTick(t, store, w, p)
	if p.State != "paused" {
		t.Fatal("pause", p.State)
	}
}

func TestScheduledQualityCoverageUnknownAndTenantReferences(t *testing.T) {
	s, store, w, source, target, p := scheduledFixture(t)
	p = scheduledTick(t, store, w, p)
	if _, err := target.Exec(t.Context(), `UPDATE orders SET total=10`); err != nil {
		t.Fatal(err)
	}
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_quality"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Quality.State != "passed" {
		t.Fatal("quality recovery", p.OperationsStatus.Quality)
	}
	if _, err := target.Exec(t.Context(), `INSERT INTO orders(id,tenant_id,total) SELECT i,'a',10 FROM generate_series(3,10002) i`); err != nil {
		t.Fatal(err)
	}
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_quality"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Quality.State != "inconclusive" || !p.OperationsStatus.Quality.Results[0].CountIsLowerBound {
		t.Fatal("sample declared complete", p.OperationsStatus.Quality)
	}
	if _, err := target.Exec(t.Context(), `ALTER POLICY gregale_pipeline_read ON orders USING(false)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "run_quality"}); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.OperationsStatus.Quality.State != "unknown" {
		t.Fatal("closed gate reported quality pass", p.OperationsStatus.Quality)
	}
	encoded, _ := json.Marshal(p.DataPipeline)
	if strings.Contains(string(encoded), "private-row-sentinel") || strings.Contains(string(encoded), "9007199254740993.123456789") {
		t.Fatal("quality disclosed row values")
	}
	_ = source
	// A cross-tenant match is a reference violation even for the worker's
	// privileged aggregate connection.
	r := relation{Name: "orders", Columns: []column{{Name: "id", Type: "int8", Key: true, NotNull: true}, {Name: "tenant_id", Type: "text", NotNull: true}, {Name: "parent_id", Type: "int8"}}}
	q, args, err := qualityQuery(p, api.DataPipelineQualityRule{Name: "parent", Kind: "reference", Table: "orders", Columns: []string{"parent_id"}, ReferenceTable: "orders", ReferenceColumns: []string{"id"}}, []relation{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(t.Context(), `ALTER TABLE orders ADD COLUMN parent_id bigint; UPDATE orders SET parent_id=2 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var more bool
	var violations int64
	if err = target.QueryRow(t.Context(), q, args...).Scan(&more, &violations); err != nil || violations != 1 {
		t.Fatal("cross-tenant reference counted as valid", violations, err)
	}
}

func TestScheduledSnapshotPublishesAllTablesUnderTheFinalFence(t *testing.T) {
	_, store, w, source, target, p := scheduledFixture(t)
	c, err := store.Connection(t.Context(), p.AccountID, p.SourceConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := openConnection(w.Identities, c)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := w.Engine.connectSource(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var role string
	if err = reader.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if _, err = source.Exec(t.Context(), `CREATE TABLE customers(id bigint PRIMARY KEY,tenant_id text NOT NULL,name text); INSERT INTO customers VALUES(1,'a','before'); GRANT SELECT ON customers TO `+ident(role)); err != nil {
		t.Fatal(err)
	}
	// Arrange an initial two-table intent before any endpoint work starts.
	if _, err = store.pool.Exec(t.Context(), `UPDATE data_pipelines SET tables='["customers","orders"]',serving_config=jsonb_set(serving_config,'{columns,customers}','["id","tenant_id","name"]') WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	p = scheduledTick(t, store, w, p)
	if p.State != "live" {
		t.Fatal("two-table capture", p.Reason)
	}
	if _, err = source.Exec(t.Context(), `UPDATE customers SET name='after'; UPDATE orders SET total=30`); err != nil {
		t.Fatal(err)
	}
	p.OperationsStatus.Snapshot.State = "running"
	p.OperationsStatus.Snapshot.RunID = uuid.NewString()
	fences := 0
	ob := w.Engine.reconcileSnapshot(workloadContext(t.Context(), p), p, raw, func(context.Context) error {
		fences++
		if fences == 2 {
			return ErrConflict
		}
		return nil
	})
	if fences != 2 || ob.OperationsStatus.Snapshot.State != "failed" {
		t.Fatal("final catalog fence was not enforced", fences, ob)
	}
	var name, total string
	if err = target.QueryRow(t.Context(), `SELECT (SELECT name FROM customers WHERE id=1),(SELECT total::text FROM orders WHERE id=2)`).Scan(&name, &total); err != nil || name != "before" || total != "-1" {
		t.Fatal("partial multi-table publication escaped rollback", name, total, err)
	}
}

func TestScheduledNativeVerificationHonorsWindowCancellationAndBatchBudget(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id bigint PRIMARY KEY,value text); INSERT INTO public.orders SELECT i,'value' FROM generate_series(1,10000) i`)
	now := time.Now().UTC()
	minute := now.Hour()*60 + now.Minute()
	c := &api.DataPipelineOperations{Version: 1, Workload: &api.DataPipelineWorkload{BatchesPerTick: 1, QueryTimeoutMillis: 2000, Window: &api.DataPipelineWorkWindow{StartMinuteUTC: (minute + 2) % 1440, EndMinuteUTC: (minute + 3) % 1440}}}
	configure := func() {
		t.Helper()
		p := f.pipeline
		if err := f.service.Operations(t.Context(), p.AccountID, p.ID, api.DataPipelineOperationsRequest{ExpectedRevision: p.Revision, Action: "configure", Config: c}); err != nil {
			t.Fatal(err)
		}
		var err error
		f.pipeline, err = f.store.Get(t.Context(), p.AccountID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	configure()
	f.control(t, "verify")
	f.tick(t)
	p := f.pipeline
	if !p.VerifyRequested || p.VerificationProgress == nil || p.VerificationProgress.Rows != 0 {
		t.Fatal("native verification ran outside the window", p.VerificationProgress)
	}
	if err := f.service.CancelVerification(t.Context(), p.AccountID, p.ID, p.Revision, p.VerificationProgress.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.VerifyRequested || f.pipeline.VerificationProgress.State != "cancelled" {
		t.Fatal("work window prevented cancellation", f.pipeline.VerificationProgress)
	}
	c.Workload.Window = nil
	configure()
	f.control(t, "verify")
	f.tick(t)
	v := f.pipeline.VerificationProgress
	if v == nil || v.Rows < 1 || v.Rows > api.DataPipelineVerificationChunkRows || !f.pipeline.VerifyRequested {
		t.Fatal("native verification exceeded a one-batch tick", v)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Verification == "passed" && !p.VerifyRequested })
}
