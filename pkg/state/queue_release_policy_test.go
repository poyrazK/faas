package state_test

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueReleasePolicyMem(t *testing.T) { queueReleasePolicySuite(t, state.NewMemStore()) }
func TestQueueReleasePolicyPG(t *testing.T)  { s, _ := pgStore(t); queueReleasePolicySuite(t, s) }

func queueReleasePolicySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, serving, candidate := bindingPromotionFixture(t, s)
	policies, gate := s.(state.BindingReleasePolicyStore), s.(state.BindingPromotionStore)
	zero := int64(0)
	requirements := map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
	p, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, QueueRequirements: requirements})
	if err != nil || !maps.Equal(p.QueueRequirements, requirements) {
		t.Fatalf("queue policy not stored: %+v %v", p, err)
	}
	delete(requirements, "orders")
	delete(p.QueueRequirements, "orders")
	p, err = policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
	if err != nil || p.QueueRequirements["orders"] != api.QueueEvidenceConsumerReadiness {
		t.Fatalf("policy mutated without revision: %+v %v", p, err)
	}
	fence := bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision, fence.MaxVerificationAge = p.Revision, api.DefaultBindingVerificationAge
	for _, wrong := range []map[string]string{nil, {}, {"other": api.QueueEvidenceConsumerReadiness}} {
		fence.QueueRequirements = wrong
		if _, err := gate.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !state.IsBindingReleaseRequired(err) && !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("mismatched queue grant admitted: %v", err)
		}
		d, err := s.DeploymentByID(ctx, candidate.ID)
		if err != nil || d.TrafficPercent != 0 {
			t.Fatalf("blocked grant changed traffic: %+v %v", d, err)
		}
	}
	fence.QueueRequirements = maps.Clone(p.QueueRequirements)
	writeCtx := state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence})
	delete(fence.QueueRequirements, "orders")
	if _, err := s.UpdateDeploymentTraffic(writeCtx, candidate.ID, 25); err != nil {
		t.Fatalf("matching copied queue grant rejected: %v", err)
	}
	fence = bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision, fence.MaxVerificationAge, fence.QueueRequirements = p.Revision, time.Minute, maps.Clone(p.QueueRequirements)
	revision := p.Revision
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, QueueRequirements: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("changed queue policy retained grant: %v", err)
	}
}

func TestQueueReleasePolicyPGConstraintHistoryAndRollback(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := context.Background()
	a, app, _, _ := bindingPromotionFixture(t, s)
	zero := int64(0)
	p, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, QueueRequirements: map[string]string{"orders": api.QueueEvidenceConsumerReadiness}})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`null`, `[]`, `{"BAD":"consumer_readiness"}`, `{"orders":"connectivity"}`, `{"orders":null}`, `{"orders":true}`} {
		_, err := pool.Exec(ctx, "UPDATE app_binding_release_policies SET queue_requirements=$2::jsonb,revision=revision+1 WHERE app_id=$1", app.ID, invalid)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "queue_release_requirements" {
			t.Fatalf("invalid queue requirement %s bypassed CHECK: %v", invalid, err)
		}
	}
	var saved string
	if err := pool.QueryRow(ctx, "SELECT policy->'queue_requirements'->>'orders' FROM app_binding_release_policy_history WHERE app_id=$1 AND revision=1", app.ID).Scan(&saved); err != nil || saved != api.QueueEvidenceConsumerReadiness {
		t.Fatalf("queue policy history missing: %q %v", saved, err)
	}
	data, err := migrations.FS.ReadFile("20261016150020001_queue_release_requirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(data), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration rollback")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, down)
	_ = tx.Rollback(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "queue_release_rollback" {
		t.Fatalf("rollback silently removed requirements: %v", err)
	}
	revision := p.Revision
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, QueueRequirements: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	var empty bool
	if err := tx.QueryRow(ctx, "SELECT queue_requirements='{}'::jsonb FROM app_binding_release_policies WHERE app_id=$1", app.ID).Scan(&empty); err != nil || !empty {
		t.Fatalf("reinstallation lost explicit empty policy: %t %v", empty, err)
	}
}
