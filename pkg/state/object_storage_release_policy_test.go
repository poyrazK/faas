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

func TestObjectStorageReleasePolicyMem(t *testing.T) {
	objectStorageReleasePolicySuite(t, state.NewMemStore())
}
func TestObjectStorageReleasePolicyPG(t *testing.T) {
	s, _ := pgStore(t)
	objectStorageReleasePolicySuite(t, s)
}

func objectStorageReleasePolicySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, serving, candidate := bindingPromotionFixture(t, s)
	policies := s.(state.BindingReleasePolicyStore)
	zero := int64(0)
	requirements := map[string]string{"ASSETS": "read_write"}
	p, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, ObjectStorageRequirements: requirements})
	if err != nil || !maps.Equal(p.ObjectStorageRequirements, requirements) {
		t.Fatalf("requirements not stored: %+v %v", p, err)
	}
	// Returned and submitted maps cannot mutate policy behind its revision fence.
	requirements["ASSETS"] = "read"
	p.ObjectStorageRequirements["ASSETS"] = "write"
	p, err = policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
	if err != nil || p.ObjectStorageRequirements["ASSETS"] != "read_write" {
		t.Fatalf("policy mutated without CAS: %+v %v", p, err)
	}
	gate := s.(state.BindingPromotionStore)
	fence := bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision, fence.MaxVerificationAge = p.Revision, api.DefaultBindingVerificationAge
	for _, wrong := range []map[string]string{nil, {}, {"ASSETS": "read"}, {"OTHER": "read_write"}, {"ASSETS": "write"}} {
		fence.ObjectStorageRequirements = wrong
		if _, err := gate.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !state.IsBindingReleaseRequired(err) && !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("weaker/mismatched fence admitted: %v", err)
		}
		d, err := s.DeploymentByID(ctx, candidate.ID)
		if err != nil || d.TrafficPercent != 0 {
			t.Fatalf("blocked request changed traffic: %+v %v", d, err)
		}
	}
	fence.ObjectStorageRequirements = maps.Clone(p.ObjectStorageRequirements)
	writeCtx := state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence})
	fence.ObjectStorageRequirements["ASSETS"] = "read"
	if _, err := s.UpdateDeploymentTraffic(writeCtx, candidate.ID, 25); err != nil {
		t.Fatalf("matching checked partial traffic rejected: %v", err)
	}
	fence = bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision, fence.MaxVerificationAge, fence.ObjectStorageRequirements = p.Revision, time.Minute, maps.Clone(p.ObjectStorageRequirements)
	revision := p.Revision
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, ObjectStorageRequirements: map[string]string{"ASSETS": "write"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("policy change did not invalidate observation: %v", err)
	}
}

func TestObjectStorageReleasePolicyPGConstraintAndHistory(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := context.Background()
	a, app, _, _ := bindingPromotionFixture(t, s)
	zero := int64(0)
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, ObjectStorageRequirements: map[string]string{"ASSETS": "write"}}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`null`, `[]`, `{"bad":"write"}`, `{"ASSETS":"delete"}`, `{"ASSETS":null}`, `{"ASSETS":true}`} {
		_, err := pool.Exec(ctx, "UPDATE app_binding_release_policies SET object_storage_requirements=$2::jsonb,revision=revision+1 WHERE app_id=$1", app.ID, invalid)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_storage_release_requirements" {
			t.Fatalf("invalid policy %s bypassed CHECK: %v", invalid, err)
		}
	}
	var saved string
	if err := pool.QueryRow(ctx, "SELECT policy->'object_storage_requirements'->>'ASSETS' FROM app_binding_release_policy_history WHERE app_id=$1 AND revision=1", app.ID).Scan(&saved); err != nil || saved != "write" {
		t.Fatalf("durable storage policy history missing: %q %v", saved, err)
	}
}

func TestObjectStorageReleasePolicyMigrationRoundTrip(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := context.Background()
	a, app, _, _ := bindingPromotionFixture(t, s)
	zero := int64(0)
	p, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, ObjectStorageRequirements: map[string]string{"ASSETS": "write"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrations.FS.ReadFile("20261011130000001_object_storage_release_requirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, parts[1])
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_storage_release_rollback" {
		t.Fatalf("rollback silently loosened configured policy: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	revision := p.Revision
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, ObjectStorageRequirements: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
	var valid bool
	if err := tx.QueryRow(ctx, "SELECT valid_object_storage_release_requirements('{\"ASSETS\":\"read_write\"}'::jsonb) AND NOT valid_object_storage_release_requirements('{\"ASSETS\":\"delete\"}'::jsonb)").Scan(&valid); err != nil || !valid {
		t.Fatalf("reapplied validation: %t %v", valid, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestObjectStorageReleasePolicyMigrationRejectsConcurrentRequirements(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, app, _, _ := bindingPromotionFixture(t, s)
	zero := int64(0)
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	data, err := migrations.FS.ReadFile("20261011130000001_object_storage_release_requirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.SplitN(string(data), "-- +goose Down", 2)[1]
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if _, err := writer.Exec(ctx, `UPDATE app_binding_release_policies SET object_storage_requirements='{"ASSETS":"write"}',revision=revision+1 WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	rollback, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rollback.Rollback(context.Background()) }()
	pid := rollback.Conn().PgConn().PID()
	finished := make(chan error, 1)
	go func() {
		_, err := rollback.Exec(ctx, down)
		finished <- err
	}()
	// Wait for the migration to contend with the uncommitted policy writer.
	// Without a lock before its guard, it sees the old empty map and then waits
	// in ALTER TABLE; committing the writer would silently erase requirements.
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1::bigint`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("migration did not wait for the active policy writer: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_storage_release_rollback" {
			t.Fatalf("rollback discarded concurrent storage requirements: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := rollback.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
	if err != nil || p.ObjectStorageRequirements["ASSETS"] != "write" {
		t.Fatalf("concurrent storage requirements lost: %+v %v", p, err)
	}
}
