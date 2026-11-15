package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func evolutionFixture(t *testing.T) (*Service, *PostgresStore, Pipeline) {
	t.Helper()
	s, store, account, c, r := setupFixture(t)
	metadata, _ := json.Marshal([]api.DataSourceTable{servingTable()})
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET result=jsonb_set(result,'{tables}',$2::jsonb) WHERE connection_id=$1`, c.ID, metadata); err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: r.Name, SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: r.Tables, Serving: servingFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(t.Context(), `UPDATE data_pipelines SET state='live',initialized=true,observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), account, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, saved
}

func TestEvolutionFrozenProjectionAndPreviousWorkerFence(t *testing.T) {
	s, store, p := evolutionFixture(t)
	r := api.DataPipelineEvolutionRequest{ExpectedRevision: p.Revision, Action: "preview", Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status", "secret"}}}
	if err := s.Evolve(t.Context(), uuid.NewString(), p.ID, r); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign intent", err)
	}
	if err := s.Evolve(t.Context(), p.AccountID, p.ID, r); err != nil {
		t.Fatal(err)
	}
	preview, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Evolution == nil || preview.Serving.Version != 1 || len(preview.Serving.Columns["orders"]) != 3 {
		t.Fatal("preview mutated serving", preview)
	}
	for _, req := range []api.DataPipelineEvolutionRequest{
		{ExpectedRevision: p.Revision, Action: "approve", ChangeID: preview.Evolution.ID},
		{ExpectedRevision: preview.Revision, Action: "approve", ChangeID: "different"},
		{ExpectedRevision: preview.Revision, Action: "approve", ChangeID: preview.Evolution.ID, Columns: r.Columns},
	} {
		if err = s.Evolve(t.Context(), p.AccountID, p.ID, req); err == nil {
			t.Fatal("approval accepted replacement or stale intent")
		}
	}
	if err = s.Evolve(t.Context(), p.AccountID, p.ID, api.DataPipelineEvolutionRequest{ExpectedRevision: preview.Revision, Action: "approve", ChangeID: preview.Evolution.ID}); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"", "customer-serving-v1", "pipeline-operability-v1"} {
		tx, err := store.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), `SELECT set_config('gregale.pipeline_worker_protocol',$1,true)`, protocol); err != nil {
			t.Fatal(err)
		}
		_, err = store.q.PipelineClaim(t.Context(), tx, "old-worker")
		_ = tx.Rollback(t.Context())
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("old worker acquired evolved projection", protocol, err)
		}
	}
	claimed, err := store.Claim(t.Context(), "current-worker")
	if err != nil || claimed.Serving.Version != 2 || claimed.Evolution.State != "approved" {
		t.Fatal(claimed, err)
	}
}

func TestEvolutionRejectsRemovalMixedChangesAndUnsafeRelationships(t *testing.T) {
	_, _, p := evolutionFixture(t)
	for _, r := range []api.DataPipelineEvolutionRequest{
		{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id"}}},
		{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status", "secret"}, "other": {"id", "tenant_id"}}},
		{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status"}, "not-safe;sql": {"id", "tenant_id"}}},
		{Kind: "relationships", Relationships: []api.DataPipelineRelationship{{Name: "hidden", SourceTable: "orders", TargetTable: "orders", SourceColumns: []string{"secret"}, TargetColumns: []string{"id"}}}},
	} {
		if _, err := evolutionPlan(p, r); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid evolution accepted", r, err)
		}
	}
}

func TestAuthReaderProofIsDurableAndCannotRenewInGrace(t *testing.T) {
	_, store, p := evolutionFixture(t)
	a := &api.DataPipelineAuthOrganizations{AuthAppID: uuid.NewString(), BindingID: uuid.NewString(), DatabaseID: uuid.NewString(), Mappings: []api.DataPipelineOrganizationTenant{{OrganizationID: uuid.NewString(), Tenant: "a"}}}
	p.Serving.Version, p.Serving.AuthOrganizations = 2, a
	raw, _ := json.Marshal(p.Serving)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET serving_config=$2 WHERE id=$1`, p.ID, raw); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = store.Claim(t.Context(), "auth-worker")
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{Store: store, Allowed: func(context.Context, string) bool { return true }, AuthMemberships: func(context.Context, Pipeline) (AuthMembershipSnapshot, error) {
		return AuthMembershipSnapshot{Members: []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}, Issuer: "https://auth.example/auth/v1"}, nil
	}}
	if err = w.syncAuthMemberships(t.Context(), &p); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReaderChange == nil || saved.ReaderChange.State != "approved" || saved.ReaderChange.AfterIssuer != "https://auth.example/auth/v1" || !slices.Equal(saved.ReaderChange.After, []string{"alice"}) {
		t.Fatal("snapshot not durable", saved.ReaderChange)
	}
	until, err := time.Parse(time.RFC3339Nano, saved.ReaderChange.AfterValidUntil)
	if err != nil || time.Until(until) > api.DataPipelineAuthSnapshotTTL {
		t.Fatal("unbounded proof", until, err)
	}
	// The owner read p before this snapshot was staged. The control SQL must
	// fence that stale read even though automatic membership work keeps revision.
	_, err = store.q.PipelineControl(t.Context(), store.pool, sqlc.PipelineControlParams{
		AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, DesiredState: "deleted", State: p.State, QueuedVerification: []byte("{}"),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("delete raced a newly approved automatic grant", err)
	}
	// A later delivery cannot replace a previously committed policy intent.
	old := saved.ReaderChange.ID
	w.AuthMemberships = func(context.Context, Pipeline) (AuthMembershipSnapshot, error) {
		t.Fatal("pending approval was replaced")
		return AuthMembershipSnapshot{}, nil
	}
	if err = w.syncAuthMemberships(t.Context(), &saved); err != nil || saved.ReaderChange.ID != old {
		t.Fatal(err)
	}
	// The quota/grace boundary permits a strict reduction, never renewal/addition.
	appliedProof := &ReaderChange{State: "applied", AfterIssuer: "https://auth.example/auth/v1"}
	saved.ReaderChange = appliedProof
	w.AuthCanGrant = func(context.Context, string) bool { return false }
	w.AuthMemberships = func(context.Context, Pipeline) (AuthMembershipSnapshot, error) {
		return AuthMembershipSnapshot{Members: tenantAccess(saved).Memberships, Issuer: "https://auth.example/auth/v1"}, nil
	}
	if err = w.syncAuthMemberships(t.Context(), &saved); err != nil || saved.ReaderChange != appliedProof {
		t.Fatal("renewed proof in grace", err)
	}
	// A namespace change during grace must revoke every existing subject,
	// even if the new app happens to contain the same subject/tenant pairs.
	if _, err = store.pool.Exec(t.Context(), `UPDATE data_pipelines SET reader_change='{}' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	saved.ReaderChange = &ReaderChange{State: "applied", AfterIssuer: "https://old-auth.example/auth/v1"}
	if err = w.syncAuthMemberships(t.Context(), &saved); err != nil || saved.ReaderChange.State != "approved" || len(saved.ReaderChange.AfterMemberships) != 0 || !readerReduction(*saved.ReaderChange) {
		t.Fatal("issuer change granted access in grace", saved.ReaderChange, err)
	}
}

func TestAuthProofExpiryOverridesPreviouslyHealthyReplication(t *testing.T) {
	now := time.Now().UTC()
	p := api.DataPipeline{State: "live", DesiredState: "running", ObservationFresh: true, Verification: "passed", Tables: []string{"orders"}, TablesReady: 1, Serving: &api.DataPipelineServing{Version: 2, AuthOrganizations: &api.DataPipelineAuthOrganizations{}, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", ValidUntil: now.Format(time.RFC3339Nano)}}}
	if h := pipelineHealth(p, now); h.Code != "auth_membership_unconfirmed" || h.State == "healthy" {
		t.Fatal("expired authorization stayed green", h)
	}
	p.Serving.TenantAccess.ValidUntil = now.Add(time.Minute).Format(time.RFC3339Nano)
	if h := pipelineHealth(p, now); h.Code == "auth_membership_unconfirmed" {
		t.Fatal("current proof was rejected", h)
	}
}
