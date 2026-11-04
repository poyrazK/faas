package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func TestRestrictedReaderAdmissionCannotApproveNewExposure(t *testing.T) {
	s, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET read_subjects='["alice","bob","carol"]' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	deploy := false
	s.CanCreate = func(context.Context, string) bool { return deploy }
	get := func() Pipeline {
		t.Helper()
		v, err := store.Get(ctx, p.AccountID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, subjects := range [][]string{{"alice", "mallory"}, {"alice", "bob", "carol", "mallory"}} {
		if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "preview", "", subjects); !errors.Is(err, ErrUnavailable) {
			t.Fatal("restricted account gained a reader", err)
		}
	}
	deploy = true
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "preview", "", []string{"alice", "mallory"}); err != nil {
		t.Fatal(err)
	}
	p = get()
	deploy = false
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "approve", p.ReaderChange.ID, []string{"alice"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("approval trusted caller subjects or stale deployment admission", err)
	}
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "discard", "", nil); err != nil {
		t.Fatal("restricted account could not discard grant", err)
	}
	p = get()
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "preview", "", []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	p = get()
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "approve", p.ReaderChange.ID, []string{"mallory"}); err != nil {
		t.Fatal("pure reduction rejected", err)
	}
	if !slices.Equal(get().ReaderChange.After, []string{"alice"}) {
		t.Fatal("approval changed stored reduction")
	}
	s.Allowed = func(context.Context, string) bool { return false }
	if err := s.Readers(ctx, p.AccountID, p.ID, p.Revision, "discard", "", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("operator admission bypassed", err)
	}
}

func TestTableRetirementRefreshRetryIsOwnedFreshBoundedAndFenced(t *testing.T) {
	for _, kind := range []string{"", "live"} {
		t.Run("kind="+kind, func(t *testing.T) {
			service, store, p, _ := schemaPipelineFixture(t)
			ctx := t.Context()
			plan := cancellationTestPlan(p)
			plan.Retry = api.DataPipelineSchemaRetriesMax
			c := &TableCancellation{Kind: kind, Table: "customers", RequestID: uuid.NewString(), Retention: "retain_private", State: "approved", Stage: "archived", TableOID: 10, SourceTableOID: 11, SubscriptionOID: 12, PublicationOID: 13}
			plan.Cancellation = c
			p.SchemaApproval, p.SchemaChange = plan.ID, &plan
			c.ID = tableCancellationID(p, *c)
			encoded, _ := json.Marshal(plan)
			if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,schema_approval=$3,reason='schema_api_refresh_failed',observed_revision=revision,observed_at=clock_timestamp(),retry_at=clock_timestamp() WHERE id=$1`, p.ID, encoded, plan.ID); err != nil {
				t.Fatal(err)
			}
			params := sqlc.PipelineRetryTableRetirementParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, CancellationID: c.ID, MaxRetries: int32(api.DataPipelineSchemaRetriesMax), MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())}
			for _, mutation := range []string{"desired_state='paused'", "state='live'", "reason='schema_api_refresh_pending'", "schema_approval=''", "observed_revision=revision-1", "observed_at=clock_timestamp()-interval '1 day'", `schema_change=jsonb_set(schema_change,'{Cancellation,Stage}','"detached"')`, `schema_change=jsonb_set(schema_change,'{Cancellation,State}','"pending"')`, `schema_change=jsonb_set(schema_change,'{Cancellation,Retention}','"delete"')`, `reader_change='{"State":"approved"}'`} {
				tx, err := store.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(ctx, "UPDATE data_pipelines SET "+mutation+" WHERE id=$1", p.ID); err != nil {
					t.Fatal(err)
				}
				n, err := store.q.PipelineRetryTableRetirement(ctx, tx, params)
				_ = tx.Rollback(ctx)
				if err != nil || n != 0 {
					t.Fatal("unsafe retirement refresh accepted", mutation, n, err)
				}
			}
			retry := service.TableCancellation
			wrong := service.TableRemoval
			if kind == "live" {
				retry, wrong = wrong, retry
			}
			if err := wrong(ctx, p.AccountID, p.ID, p.Revision, "", c.ID); !errors.Is(err, ErrConflict) {
				t.Fatal("wrong retirement kind accepted", err)
			}
			for _, tc := range []struct {
				account, id, approval string
				revision              int64
			}{{uuid.NewString(), p.ID, c.ID, p.Revision}, {p.AccountID, uuid.NewString(), c.ID, p.Revision}, {p.AccountID, p.ID, plan.ID, p.Revision}, {p.AccountID, p.ID, c.ID, p.Revision + 1}} {
				if err := retry(ctx, tc.account, tc.id, tc.revision, "", tc.approval); !errors.Is(err, ErrConflict) {
					t.Fatal("foreign or stale retry accepted", err)
				}
			}
			leased, err := store.Claim(ctx, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if err = retry(ctx, p.AccountID, p.ID, p.Revision, "", c.ID); err != nil {
				t.Fatal(err)
			}
			if err = store.Finish(ctx, leased, Observation{State: "live"}); !errors.Is(err, ErrConflict) {
				t.Fatal("stale worker overwrote retry", err)
			}
			next, err := store.Get(ctx, p.AccountID, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if next.SchemaChange.Cancellation.RefreshRetry != 1 || next.SchemaChange.Cancellation.ID != c.ID || next.SchemaChange.Retry != plan.Retry || next.Generation != p.Generation {
				t.Fatal("refresh retry altered retirement or copy approval")
			}
			if err = retry(ctx, p.AccountID, p.ID, next.Revision, "", c.ID); !errors.Is(err, ErrConflict) {
				t.Fatal("unobserved retry duplicated", err)
			}
			if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=jsonb_set(schema_change,'{Cancellation,RefreshRetry}',to_jsonb($2::integer)),observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID, api.DataPipelineSchemaRetriesMax); err != nil {
				t.Fatal(err)
			}
			if err = retry(ctx, p.AccountID, p.ID, next.Revision, "", c.ID); !errors.Is(err, ErrConflict) {
				t.Fatal("exhausted refresh retry accepted", err)
			}
		})
	}
}
