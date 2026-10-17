package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func TestTableRecoveryApprovalIsOwnedFreshExactAndFenced(t *testing.T) {
	s, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	s.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	before := schemaTestRelations()
	added := relation{Name: "customers", OID: 300, Columns: before[0].Columns}
	plan := tablePlan(p, before, append([]relation{added}, before...))
	plan.State = "verifying"
	encoded, _ := json.Marshal(plan)
	progress, _ := json.Marshal(VerificationProgress{ID: strings.Repeat("a", 64), State: "mismatch", Table: "customers"})
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET tables='["customers","orders"]',schema_change=$2,schema_approval=$3,verification_progress=$4,reason='replica_mismatch',retry_at=clock_timestamp() WHERE id=$1`, p.ID, encoded, plan.ID, progress); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, mutation string }{
		{"healthy", "reason='table_addition_verifying'"},
		{"running proof", `verification_progress=jsonb_set(verification_progress,'{State}','"running"')`},
		{"baseline table", `verification_progress=jsonb_set(verification_progress,'{Table}','"orders"')`},
		{"unapproved", "schema_approval=''"},
		{"completed", `schema_change=jsonb_set(schema_change,'{State}','"complete"')`},
		{"stale observation", "observed_at=clock_timestamp()-interval '1 day'"},
		{"paused", "desired_state='paused'"},
		{"limit", `schema_change=jsonb_set(schema_change,'{Retry}','10')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := store.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err = tx.Exec(ctx, "UPDATE data_pipelines SET "+tc.mutation+" WHERE id=$1", p.ID); err != nil {
				t.Fatal(err)
			}
			// Exercise the same SQLC predicates inside the rolled-back fixture.
			n, err := store.q.PipelineRequestTableRecovery(ctx, tx, sqlc.PipelineRequestTableRecoveryParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, TableName: "customers", MaxRetries: api.DataPipelineSchemaRetriesMax, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
			if err != nil || n != 0 {
				t.Fatal("unsafe recovery request accepted", n, err)
			}
		})
	}
	for _, tc := range []struct {
		account string
		rev     int64
	}{{uuid.NewString(), p.Revision}, {p.AccountID, p.Revision + 1}} {
		if err := s.TableRecovery(ctx, tc.account, p.ID, tc.rev, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign/stale recovery accepted", err)
		}
	}
	leasing, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, p.Revision, ""); err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leasing, Observation{State: "live"}); !errors.Is(err, ErrConflict) {
		t.Fatal("old worker overwrote recovery", err)
	}
	requested, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || requested.SchemaChange.Recovery.State != "requested" || requested.Generation != p.Generation || len(requested.Tables) != 2 {
		t.Fatal("preview changed native intent", err)
	}
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("unobserved preview approved", err)
	}
	r := TableRecovery{State: "pending", Stage: "preview", Table: "customers", TableOID: 401, SubscriptionOID: 402, PublicationOID: 403, Attempt: 1}
	p.SchemaApproval = plan.ID
	r.ID = tableRecoveryID(p, r)
	plan.Recovery = &r
	encoded, _ = json.Marshal(plan)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_recovery_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID, encoded); err != nil {
		t.Fatal(err)
	}
	for _, approval := range []string{strings.Repeat("b", 64), "invalid"} {
		if err = s.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, approval); err == nil {
			t.Fatal("different preview approved")
		}
	}
	s.CapacityStatus = nil
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, r.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unknown capacity accepted", err)
	}
	s.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, r.ID); err != nil {
		t.Fatal(err)
	}
	approved, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || approved.SchemaChange.Retry != 1 || approved.SchemaChange.Recovery.State != "approved" || approved.VerificationProgress != nil || approved.Generation != p.Generation {
		t.Fatal("approval did not install fresh attempt", err)
	}
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("approval replay advanced attempt", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveSchema(ctx, p.AccountID, p.ID, approved.Revision, plan.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("schema retry bypassed active recovery", err)
	}
}

func TestTableRecoveryIDBindsNativeIdentitiesAndAttempt(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 2}, SchemaApproval: strings.Repeat("a", 64)}
	r := TableRecovery{Table: "customers", TableOID: 1, PublicationOID: 2, SubscriptionOID: 3, Attempt: 1}
	id := tableRecoveryID(p, r)
	for _, mutate := range []func(*Pipeline, *TableRecovery){
		func(p *Pipeline, _ *TableRecovery) { p.Generation++ }, func(p *Pipeline, _ *TableRecovery) { p.SchemaApproval = strings.Repeat("b", 64) },
		func(_ *Pipeline, r *TableRecovery) { r.Table = "orders" }, func(_ *Pipeline, r *TableRecovery) { r.TableOID++ },
		func(_ *Pipeline, r *TableRecovery) { r.SubscriptionOID++ }, func(_ *Pipeline, r *TableRecovery) { r.PublicationOID++ }, func(_ *Pipeline, r *TableRecovery) { r.Attempt++ },
	} {
		copyP, copyR := p, r
		mutate(&copyP, &copyR)
		if tableRecoveryID(copyP, copyR) == id {
			t.Fatal("recovery identity omitted approval boundary")
		}
	}
}
