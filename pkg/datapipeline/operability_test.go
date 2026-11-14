package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestFreshnessConfirmationTruthAndBoundaries(t *testing.T) {
	now := time.Now().UTC()
	base := api.DataPipeline{State: "live", DesiredState: "running", ObservationFresh: true, ObservedAt: now.Format(time.RFC3339Nano), FreshnessTargetSeconds: 30, AppliedThroughAt: now.Add(-30 * time.Second).Format(time.RFC3339Nano)}
	for _, tc := range []struct {
		name, want string
		mutate     func(*api.DataPipeline)
	}{
		{"boundary", "confirmed", func(*api.DataPipeline) {}},
		{"older by nanosecond", "unconfirmed", func(p *api.DataPipeline) {
			p.AppliedThroughAt = now.Add(-30*time.Second - time.Nanosecond).Format(time.RFC3339Nano)
		}},
		{"idle source without confirmation", "unconfirmed", func(p *api.DataPipeline) { p.AppliedThroughAt = "" }},
		{"stale worker", "unknown", func(p *api.DataPipeline) {
			p.ObservedAt = now.Add(-api.DataPipelineObservationMaxAge).Format(time.RFC3339Nano)
		}},
		{"future checkpoint", "unconfirmed", func(p *api.DataPipeline) { p.AppliedThroughAt = now.Add(time.Second).Format(time.RFC3339Nano) }},
		{"revision not observed", "unknown", func(p *api.DataPipeline) { p.ObservationFresh = false }},
		{"paused", "inactive", func(p *api.DataPipeline) { p.DesiredState = "paused" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			f := FreshnessStatus(p, now)
			if f == nil || f.State != tc.want {
				t.Fatalf("%+v", f)
			}
		})
	}
	base.FreshnessTargetSeconds = 0
	if FreshnessStatus(base, now) != nil {
		t.Fatal("disabled target emitted status")
	}
	for _, n := range []int64{-1, 1, 14, 86401} {
		if validFreshnessTarget(n) {
			t.Fatal("invalid target", n)
		}
	}
	for _, n := range []int64{0, 15, 86400} {
		if !validFreshnessTarget(n) {
			t.Fatal("valid target", n)
		}
	}
}

func TestSetupPlanHasNoAllocationAndReusesImmutableJob(t *testing.T) {
	service, store, account, _, req := setupFixture(t)
	allocations := 0
	service.EnsureMirror = func(context.Context, Setup) error { allocations++; return nil }
	service.PrepareDataAPI = func(context.Context, Setup) (string, bool, error) { allocations++; return "", false, nil }
	req.FreshnessTargetSeconds = 30
	plan, err := service.PlanSetup(t.Context(), account, req)
	if err != nil || !plan.CreatesMirror || plan.StorageLimitBytes != 1<<30 || len(plan.BillingNotes) < 3 {
		t.Fatal(plan, err)
	}
	jobs, err := store.Setups(t.Context(), account)
	if err != nil || len(jobs) != 0 || allocations != 0 {
		t.Fatal("plan allocated or saved intent", jobs, err)
	}
	job, err := service.StartSetup(t.Context(), account, req)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = service.PlanSetup(t.Context(), account, req)
	if err != nil || plan.ExistingSetupID != job.ID || plan.CreatesMirror || allocations != 0 {
		t.Fatal("recovery changed resource intent", plan, err)
	}
	req.FreshnessTargetSeconds = 60
	if _, err = service.StartSetup(t.Context(), account, req); !errors.Is(err, ErrConflict) {
		t.Fatal("mutable retry accepted", err)
	}
	// Previous serving-v1 setup owners cannot silently drop the saved target.
	tx, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), `SELECT set_config('gregale.pipeline_worker_protocol','customer-serving-v1',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.q.PipelineSetupClaim(t.Context(), tx, "previous-worker"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old setup owner acquired new intent", err)
	}
	if _, err = service.GetSetup(t.Context(), uuid.NewString(), job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign setup exposed", err)
	}
}

func TestFreshnessRevisionAndAlertWorkerFence(t *testing.T) {
	service, store, account, c, req := setupFixture(t)
	p, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: req.Name, SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: req.Tables, ReadSubjects: req.ReadSubjects})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []api.DataPipelineFreshnessRequest{{ExpectedRevision: p.Revision, TargetSeconds: 14}, {ExpectedRevision: 0, TargetSeconds: 30}} {
		if err = service.ConfigureFreshness(t.Context(), account, p.ID, r); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err = service.ConfigureFreshness(t.Context(), account, p.ID, api.DataPipelineFreshnessRequest{ExpectedRevision: p.Revision, TargetSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err = service.ConfigureFreshness(t.Context(), account, p.ID, api.DataPipelineFreshnessRequest{ExpectedRevision: p.Revision, TargetSeconds: 60}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision changed target", err)
	}
	now := time.Now().UTC()
	p.FreshnessTargetSeconds = 30
	p.State = "live"
	p.ObservedAt = now.Format(time.RFC3339Nano)
	p.ObservedRevision = p.Revision
	p.ObservationFresh = true
	p.AppliedThroughAt = now.Add(-time.Minute).Format(time.RFC3339Nano)
	if code, recovered := alertCondition(Pipeline{DataPipeline: p}, now); code != "freshness_unconfirmed" || recovered {
		t.Fatal(code, recovered)
	}
	if _, err = store.pool.Exec(t.Context(), `INSERT INTO data_pipeline_alerts(pipeline_id,account_id,enabled,ciphertext) VALUES($1,$2,true,'x')`, p.ID, account); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(t.Context(), `UPDATE data_pipeline_alerts SET check_at=clock_timestamp() WHERE pipeline_id=$1`, p.ID); err == nil {
		t.Fatal("old monitor could evaluate a target it ignores")
	}
	tx, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err = store.q.PipelineWorkerProtocol(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE data_pipeline_alerts SET candidate_code='freshness_unconfirmed',incident_code='freshness_unconfirmed' WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal("new monitor or alert code rejected", err)
	}
}

func TestIndexShapeTenantExpressionAndFrozenIdentity(t *testing.T) {
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Tables: []string{"orders"}, Serving: servingFixture()}}
	r := api.DataPipelineIndexRequest{Table: "orders", Filters: []string{"status"}, OrderBy: []api.DataPipelineIndexOrder{{Column: "id", Descending: true}}}
	keys, err := indexShape(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if got := indexKeysSQL(keys); got != `("tenant_id"::text COLLATE "C"),"status","id" DESC` {
		t.Fatal(got)
	}
	c := IndexChange{DataPipelineIndexAdvice: api.DataPipelineIndexAdvice{Table: r.Table, Keys: keys, CanApply: true}, RequestID: uuid.NewString(), Generation: 1, TableOID: 12, StorageLimitBytes: 1 << 30}
	id := indexAdviceID(p, c)
	c.ID = id
	c.State = "approved"
	c.CanApply = false
	if indexAdviceID(p, c) != id {
		t.Fatal("approval changed identity")
	}
	c.Keys[1].Column = "secret"
	if indexAdviceID(p, c) == id {
		t.Fatal("replacement columns reused approval")
	}
	r.Filters = []string{`status"; DROP TABLE orders --`}
	quoted, err := indexShape(p, r)
	if err != nil || !strings.Contains(indexKeysSQL(quoted), `"status""; DROP TABLE orders --"`) {
		t.Fatal("column name was not quoted as data", quoted, err)
	}
	r.Filters = []string{"status\x00"}
	if _, err = indexShape(p, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid PostgreSQL identifier accepted", err)
	}
	r.Filters = []string{"a", "b", "c", "d"}
	if _, err = indexShape(p, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("tenant prefix escaped key limit", err)
	}
	raw, _ := json.Marshal(c)
	if len(raw) > api.DataPipelineIndexChangeMaxBytes {
		t.Fatal("ordinary advice exceeded catalog bound")
	}
}

func TestIndexAdviceInvalidatedByGenerationAndScopedReplacement(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{Generation: 1}, IndexChange: &IndexChange{Generation: 1, SchemaChangeID: "baseline", DataPipelineIndexAdvice: api.DataPipelineIndexAdvice{State: "applied", Table: "orders"}}}
	p.SchemaChange = &SchemaChange{ID: "baseline", State: "complete"}
	if v := indexAdviceView(p, time.Now()); v.State != "applied" {
		t.Fatal("same schema invalidated installed index", v)
	}
	p.SchemaChange = &SchemaChange{ID: "repair", State: "complete", Recovery: &TableRecovery{Kind: "live", Table: "orders"}}
	if v := indexAdviceView(p, time.Now()); v.Reason != "index_mirror_replaced" || v.CanApply {
		t.Fatal("scoped replacement retained stale advice", v)
	}
	p.SchemaChange.Recovery.Table = "customers"
	if v := indexAdviceView(p, time.Now()); v.State != "applied" {
		t.Fatal("unrelated table repair invalidated installed index", v)
	}
	p.Generation = 2
	if v := indexAdviceView(p, time.Now()); v.Reason != "index_mirror_replaced" {
		t.Fatal("resync retained old generation advice", v)
	}
}

func TestFreshnessTargetIncidentAndRecoveryUseDurableMonitor(t *testing.T) {
	service, store, p, worker := alertFixture(t)
	if err := service.ConfigureFreshness(t.Context(), p.AccountID, p.ID, api.DataPipelineFreshnessRequest{ExpectedRevision: p.Revision, TargetSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	alertLive(t, store, p)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET applied_through_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if more, err := worker.CheckTick(t.Context()); err != nil || !more {
		t.Fatal(more, err)
	}
	tx, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err = store.q.PipelineWorkerProtocol(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE data_pipeline_alerts SET candidate_since=clock_timestamp()-interval '3 minutes',check_at=clock_timestamp() WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if more, err := worker.CheckTick(t.Context()); err != nil || !more {
		t.Fatal(more, err)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 1 || h[0].Code != "freshness_unconfirmed" || h[0].Kind != "incident" {
		t.Fatal("freshness incident missing", h)
	}
	current, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ConfigureFreshness(t.Context(), p.AccountID, p.ID, api.DataPipelineFreshnessRequest{ExpectedRevision: current.Revision, TargetSeconds: 0}); err != nil {
		t.Fatal(err)
	}
	alertLive(t, store, p)
	alertDue(t, t.Context(), worker, p, false)
	alertDue(t, t.Context(), worker, p, true)
	h = alertHistory(t, t.Context(), store, p)
	if len(h) != 2 || h[0].Code != "freshness_unconfirmed" || h[0].Kind != "recovery" {
		t.Fatal("disabled target did not recover after a healthy observation", h)
	}
}
