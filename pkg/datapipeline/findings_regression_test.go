// adr: 20261112220000 — pipeline findings remediation.
package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/pipelineread"
)

func TestPipelineFindingsMigrationUpgradesLegacyQueuedRequests(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE data_pipelines(
 id uuid PRIMARY KEY,generation bigint NOT NULL,read_subjects jsonb NOT NULL,
 verify_requested boolean NOT NULL,verification_progress jsonb NOT NULL,
 CONSTRAINT data_pipelines_read_subjects_check CHECK(jsonb_typeof(read_subjects)='array' AND jsonb_array_length(read_subjects) BETWEEN 1 AND 32));
 CREATE TABLE data_pipeline_schema_refreshes(id integer PRIMARY KEY);
 INSERT INTO data_pipelines SELECT md5(n::text)::uuid,2,'["alice"]',true,'{"ManualAttempt":9}' FROM generate_series(1,1002) n;
 INSERT INTO data_pipelines VALUES('00000000-0000-0000-0000-000000000001',2,'["alice"]',true,'{"ManualAttempt":9,"ID":"physical-proof","State":"running"}');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261112220000000_pipeline_reader_revocation_and_queued_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // A failed deployment can replay the partially committed migration.
		if err := pgtest.ExecMigration(ctx, t, pool, string(raw), true); err != nil {
			t.Fatal(err)
		}
	}
	var upgraded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_pipelines WHERE verification_progress->>'Phase'='queued' AND verification_progress->>'State'='waiting' AND length(verification_progress->>'ID')=64`).Scan(&upgraded); err != nil || upgraded != 1002 {
		t.Fatal("bounded backfill missed queued requests", upgraded, err)
	}
	var id, run string
	if err := pool.QueryRow(ctx, `SELECT id::text,verification_progress->>'ID' FROM data_pipelines WHERE verification_progress->>'Phase'='queued' LIMIT 1`).Scan(&id, &run); err != nil {
		t.Fatal(err)
	}
	if run != queuedVerificationID(Pipeline{DataPipeline: api.DataPipeline{ID: id, Generation: 2}}, 9) {
		t.Fatal("legacy request identity differs from new requests", run)
	}
	var physical string
	if err := pool.QueryRow(ctx, `SELECT verification_progress->>'ID' FROM data_pipelines WHERE id='00000000-0000-0000-0000-000000000001'`).Scan(&physical); err != nil || physical != "physical-proof" {
		t.Fatal("migration replaced an acquired physical proof", physical, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_pipelines SET read_subjects='[]' WHERE id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal("final-reader revocation rejected after upgrade", err)
	}
	if err := pgtest.ExecMigration(ctx, t, pool, string(raw), false); err != nil {
		t.Fatal("rollback must preserve access revocation", err)
	}
}

func TestPipelinePauseHealthRequiresAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		state string
		fresh bool
		code  string
	}{{"live", false, "pause_unconfirmed"}, {"blocked", true, "pause_unconfirmed"}, {"paused", false, "pause_unconfirmed"}, {"paused", true, "paused"}} {
		p := api.DataPipeline{DesiredState: "paused", State: tc.state, ObservationFresh: tc.fresh}
		if h := pipelineHealth(p, time.Now()); h.Code != tc.code {
			t.Fatalf("%+v -> %+v", tc, h)
		}
	}
}

func TestPipelinePausedRetentionMonitoringRemainsObservable(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		reason string
		age    time.Duration
		code   string
	}{{"", 0, ""}, {"source_unavailable", 0, "monitoring_stale"}, {"credential_unavailable", 0, "monitoring_stale"}, {"", 10 * time.Minute, "monitoring_stale"}} {
		p := Pipeline{DataPipeline: api.DataPipeline{DesiredState: "paused", State: "paused", Reason: tc.reason, Revision: 1, ObservedRevision: 1, ObservedAt: now.Add(-tc.age).Format(time.RFC3339Nano)}}
		code, recovered := alertCondition(p, now)
		if code != tc.code || recovered {
			t.Fatalf("%+v -> %s recovered=%v", tc, code, recovered)
		}
	}
}

func TestSetupRefreshesEvidenceForItsSavedSelection(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	setup, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RequestCheck(t.Context(), account, c.ID, c.Revision, "", api.RequestDataConnectionCheck{Search: "customers"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	endpoint := &api.DataEndpointCapacity{State: "ready", UsableConnections: 100, RequiredConnections: 5, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}
	if err = store.FinishCheck(t.Context(), job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "customers", Supported: true}}, Capacity: &api.DataPipelineCapacity{State: "ready", Source: endpoint}}); err != nil {
		t.Fatal(err)
	}
	saved := setupTick(t, service, store, setup.ID)
	if saved.State != "working" {
		t.Fatalf("unrelated search blocked setup: %+v", saved)
	}
	latest, err := store.Check(t.Context(), account, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.State != "pending" || !slices.Equal(latest.RequestedTables, request.Tables) {
		t.Fatalf("saved selection not refreshed: %+v", latest)
	}
}

func TestReadersCanRevokeLastSubjectDuringGrace(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	service.CanCreate = func(context.Context, string) bool { return false }
	if err := service.Readers(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", nil); err != nil {
		t.Fatal(err)
	}
	preview, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ReaderChange.After == nil || len(preview.ReaderChange.After) != 0 {
		t.Fatal("revocation was not encoded as an empty array")
	}
	if err = service.Readers(t.Context(), p.AccountID, p.ID, preview.Revision, "approve", preview.ReaderChange.ID, nil); err != nil {
		t.Fatal(err)
	}
	leased, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	applied := *leased.ReaderChange
	applied.State = "applied"
	if err = store.Finish(t.Context(), leased, Observation{ReaderOnly: true, ReaderChange: &applied}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil || len(saved.ReadSubjects) != 0 {
		t.Fatal(saved, err)
	}
	if ValidateCreate(api.CreateDataPipelineRequest{Name: "new-copy", SourceConnectionID: p.SourceConnectionID, DatabaseID: p.DatabaseID, Tables: p.Tables, ReadSubjects: nil}) == nil {
		t.Fatal("creation must still require an initial reader")
	}
}

func TestQueuedVerificationCanBeCancelledWithoutSourceAndDuringPause(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "paused"}[pause], func(t *testing.T) {
			service, store, p, _ := schemaPipelineFixture(t)
			if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET state='live',reason='',schema_change='{}',schema_approval='',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
				t.Fatal(err)
			}
			requested, err := store.Control(t.Context(), p.AccountID, p.ID, p.Revision, "verify")
			if err != nil {
				t.Fatal(err)
			}
			v := requested.VerificationProgress
			if v == nil || len(v.ID) != 64 || v.State != "waiting" || v.Phase != "queued" {
				t.Fatalf("uncancellable queue: %+v", v)
			}
			if pause {
				requested, err = store.Control(t.Context(), p.AccountID, p.ID, requested.Revision, "pause")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Control(t.Context(), p.AccountID, p.ID, requested.Revision, "verify"); !errors.Is(err, ErrConflict) {
					t.Fatal("paused verification accepted", err)
				}
			}
			service.CanCreate = func(context.Context, string) bool { return false }
			if err = service.CancelVerification(t.Context(), p.AccountID, p.ID, requested.Revision, v.ID); err != nil {
				t.Fatal("queued cancellation requires source or deployment entitlement", err)
			}
			saved, err := store.Get(t.Context(), p.AccountID, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.VerifyRequested || saved.VerificationProgress.State != "cancelled" {
				t.Fatal("queued work survived cancellation", saved)
			}
			if err = service.CancelVerification(t.Context(), p.AccountID, p.ID, requested.Revision, v.ID); !errors.Is(err, ErrConflict) {
				t.Fatal("stale cancellation accepted", err)
			}
		})
	}
}

func TestVerificationUsesSourcePrimaryKeyOrder(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(t.Context(), `CREATE TABLE public.reverse_key(tenant integer,sequence integer,payload text,PRIMARY KEY(sequence,tenant) INCLUDE(payload));
 INSERT INTO public.reverse_key SELECT i%100,i,repeat('x',64) FROM generate_series(1,100000)i;ANALYZE public.reverse_key`); err != nil {
		t.Fatal(err)
	}
	r, table, err := inspectSourceTable(t.Context(), pool, "reverse_key")
	if err != nil || !table.Supported {
		t.Fatal(table, err)
	}
	keys := primaryColumns(r)
	if len(keys) != 2 || keys[0].Name != "sequence" || keys[1].Name != "tenant" {
		t.Fatal("key order or INCLUDE handling changed", keys)
	}
	query, args := verificationQuery(r, nil, nil)
	var plan []byte
	if err = pool.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+query, args...).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plan), `"Node Type": "Seq Scan"`) || !strings.Contains(string(plan), `"Index Name": "reverse_key_pkey"`) {
		t.Fatalf("verification bypassed source index: %s", plan)
	}
	// Planner metadata must not invalidate approval identities after a restart.
	encoded, _ := json.Marshal(r)
	var persisted relation
	if err = json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if !sameVerificationRelation(r, persisted) || relationHash([]relation{r}) != relationHash([]relation{persisted}) {
		t.Fatal("planner metadata changed structural identity")
	}
	ordered, err := orderedVerificationRelations(t.Context(), pool, []relation{persisted})
	if err != nil || primaryColumns(ordered[0])[0].Name != "sequence" {
		t.Fatal("persisted plan did not recover key ordering", err)
	}
	before, err := verificationSourceStorage(t.Context(), pool, []relation{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `ALTER TABLE public.reverse_key DROP CONSTRAINT reverse_key_pkey;ALTER TABLE public.reverse_key ADD PRIMARY KEY(tenant,sequence) INCLUDE(payload)`); err != nil {
		t.Fatal(err)
	}
	after, err := verificationSourceStorage(t.Context(), pool, []relation{r})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, after) {
		t.Fatal("key ordering change did not invalidate the physical proof")
	}
	// Model DDL between planner hydration and the first storage observation.
	// The database key is already new in both observations; the query's key
	// order must independently fence cursor interpretation on the next tick.
	updated, err := orderedVerificationRelations(t.Context(), pool, []relation{persisted})
	if err != nil {
		t.Fatal(err)
	}
	hydrated, err := verificationSourceStorage(t.Context(), pool, updated)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(after, hydrated) || physicalVerificationID("same-intent", after) == physicalVerificationID("same-intent", hydrated) {
		t.Fatal("changed query order could reuse a proof created during DDL")
	}
}

func TestReadGateRefreshIsDurableAndIndependentOfSchemaRefresh(t *testing.T) {
	_, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	schema, err := os.ReadFile("catalog_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `CREATE TABLE notification_outbox(id bigserial PRIMARY KEY,channel text NOT NULL,payload text NOT NULL,available_at timestamptz NOT NULL,state text NOT NULL DEFAULT 'pending',attempts int NOT NULL DEFAULT 0,last_error text,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),delivered_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	app, dep, snap := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = store.pool.Exec(ctx, `INSERT INTO apps VALUES($1,$2,'existing-api','active');`, app, p.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO deployments VALUES($1,$2,'live','default')`, dep, app); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO snapshots VALUES($1,$2,false)`, snap, dep); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO managed_postgres_bindings VALUES($1,$2,$3,$4,'default','read_only','ready')`, uuid.NewString(), p.AccountID, p.DatabaseID, app); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = store.RefreshReadGate(ctx, leased, pipelineread.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.q.PipelineSchemaRefreshPrune(ctx, store.pool, sqlc.PipelineSchemaRefreshPruneParams{PipelineID: p.ID, ChangeID: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err = store.RefreshReadGate(ctx, leased, pipelineread.Version); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err = store.pool.QueryRow(ctx, `SELECT (SELECT count(*)=1 FROM notification_outbox) AND (SELECT count(*)=1 FROM data_pipeline_schema_refreshes WHERE read_gate) AND (SELECT stale FROM snapshots WHERE id=$1) AND EXISTS(SELECT 1 FROM app_runtime_config_changes WHERE app_id=$2)`, snap, app).Scan(&valid); err != nil || !valid {
		t.Fatal("restart was missing or replayed", valid, err)
	}
	stale := leased
	stale.Revision++
	if err = store.RefreshReadGate(ctx, stale, pipelineread.Version); err == nil {
		t.Fatal("stale worker refreshed an API")
	}
}

func TestPipelineInitialCopyStallOpensIncident(t *testing.T) {
	now := time.Now().UTC()
	p := Pipeline{DataPipeline: api.DataPipeline{DesiredState: "running", State: "copying", Reason: "initial_copy_stalled", Revision: 1, ObservedRevision: 1, ObservationFresh: true, ObservedAt: now.Format(time.RFC3339Nano)}}
	if code, _ := alertCondition(p, now); code != "sync_blocked" {
		t.Fatal("initial copy stalled without an alert", code)
	}
	if h := pipelineHealth(p.DataPipeline, now); h.State != "attention" || h.Code != "initial_copy_stalled" {
		t.Fatal(h)
	}
}

// Private target helpers are installed by the worker, before new credential publication.
func TestReadGateCredentialWaitsForInstallingHooks(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	role := "pipeline_install_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, "CREATE ROLE "+ident(role)+" LOGIN NOINHERIT"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+ident(role)+";DROP ROLE "+ident(role))
	})
	installer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = installer.Rollback(context.Background()) }()
	if err = pipelineread.Lock(ctx, installer); err != nil {
		t.Fatal(err)
	}
	if _, err = installer.Exec(ctx, "CREATE SCHEMA gregale_pipeline;"+pipelineread.Definition); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check_pipeline_read", "check_pipeline_request"} {
		if _, err = installer.Exec(ctx, "COMMENT ON FUNCTION gregale_data_api."+name+"() IS "+literal(pipelineread.Version)); err != nil {
			t.Fatal(err)
		}
	}
	credential, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = credential.Rollback(context.Background()) }()
	pid := credential.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() { done <- pipelineread.Grant(ctx, credential, role) }()
	finished := false
	defer func() {
		if !finished {
			cancel()
			<-done
		}
	}()
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			finished = true
			t.Fatal("credential escaped an uncommitted hook installation", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = installer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-done
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if err = credential.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var permitted bool
	if err = pool.QueryRow(ctx, `SELECT has_function_privilege($1,'gregale_data_api.check_pipeline_read()','EXECUTE') AND has_function_privilege($1,'gregale_data_api.check_pipeline_request()','EXECUTE') AND NOT has_schema_privilege($1,'gregale_pipeline','USAGE')`, role).Scan(&permitted); err != nil || !permitted {
		t.Fatal("credential committed without its hooks", permitted, err)
	}
}

func TestReadGateCredentialGrantIsPinnedAndIdempotent(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	role := "pipeline_grant_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, "CREATE ROLE "+ident(role)+" LOGIN NOINHERIT;CREATE SCHEMA gregale_pipeline;"+pipelineread.Definition); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), "DROP OWNED BY "+ident(role)+";DROP ROLE "+ident(role))
	})
	for _, name := range []string{"check_pipeline_read", "check_pipeline_request"} {
		if _, err := pool.Exec(ctx, "COMMENT ON FUNCTION gregale_data_api."+name+"() IS "+literal(pipelineread.Version)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = pipelineread.Grant(ctx, tx, role); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var permitted bool
	if err := pool.QueryRow(ctx, `SELECT has_function_privilege($1,'gregale_data_api.check_pipeline_read()','EXECUTE') AND NOT has_schema_privilege($1,'gregale_pipeline','USAGE')`, role).Scan(&permitted); err != nil || !permitted {
		t.Fatal("credential contract missing or private access granted", err)
	}
	if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION gregale_data_api.check_pipeline_read() RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$ BEGIN RETURN; END $$`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = pipelineread.Grant(ctx, tx, role); !errors.Is(err, pipelineread.ErrUnsafe) {
		t.Fatal("mutated helper was trusted", err)
	}
}
