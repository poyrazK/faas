package state_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCachePurgeMemory(t *testing.T) { testEnvironmentCachePurge(t, state.NewMemStore()) }
func TestEnvironmentCachePurgePostgres(t *testing.T) {
	testEnvironmentCachePurge(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testEnvironmentCachePurge(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@env-purge.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "purge"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "purge", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := s.(state.EnvironmentCachePurgeStore)
	observer := s.(state.EnvironmentCachePurgeObserver)
	sessions := s.(state.EnvironmentCacheGatewayObserver)
	req := api.CreateEnvironmentCachePurgeRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), Path: "/catalog/item"}
	first, err := store.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req)
	if err != nil || first.Generation < 1 || first.Path != req.Path {
		t.Fatalf("purge=%+v %v", first, err)
	}
	replay, err := store.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req)
	if err != nil || replay.Generation != first.Generation || !replay.RequestedAt.Equal(first.RequestedAt) {
		t.Fatal("retry changed purge", err)
	}
	altered := req
	altered.Path = ""
	if _, err = store.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("request reuse=%v", err)
	}
	if _, err = store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "production", req.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("neighbor purge leak=%v", err)
	}
	status, err := store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req.RequestID)
	if err != nil || status.Status != "degraded" {
		t.Fatalf("zero fleet=%+v %v", status, err)
	}
	role, target := "compute-node", "tcp://127.0.0.1:8080"
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "purge-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	if err = sessions.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, _ = store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req.RequestID)
	if status.Status != "pending" {
		t.Fatalf("fresh session=%+v", status)
	}
	if err = observer.RecordEnvironmentCachePurgeWatermark(ctx, node.Name, session, first.Generation); err != nil {
		t.Fatal(err)
	}
	status, _ = store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req.RequestID)
	if status.Status != "completed" || status.AcknowledgedGateways != 1 {
		t.Fatalf("ack=%+v", status)
	}
	restarted := uuid.NewString()
	if err = sessions.BeginEnvironmentCacheGatewaySession(ctx, node.Name, restarted, time.Now().UTC().Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	status, _ = store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req.RequestID)
	if status.Status != "pending" {
		t.Fatal("restart reused prior ack")
	}
	if err = observer.RecordEnvironmentCachePurgeWatermark(ctx, node.Name, session, first.Generation); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old session ack=%v", err)
	}
	// Same request concurrently commits a single command and stable generation.
	concurrent := req
	concurrent.RequestID = uuid.NewString()
	concurrent.Path = ""
	var wg sync.WaitGroup
	results := make(chan api.EnvironmentCachePurgeReceipt, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			p, e := store.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", concurrent)
			results <- p
			errs <- e
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var generation int64
	for p := range results {
		if generation != 0 && generation != p.Generation {
			t.Fatal("concurrent retry created duplicate")
		}
		generation = p.Generation
	}
	rows, err := observer.ListEnvironmentCachePurgesAfter(ctx, 0)
	if err != nil || len(rows) != 2 || rows[0].ID != first.Generation || rows[1].ID != generation {
		t.Fatalf("replay rows=%+v %v", rows, err)
	}
	if _, err = s.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := observer.ListEnvironmentCachePurgesAfter(ctx, 0)
	if err != nil || len(retained) != 2 {
		t.Fatal("soft-deleted app lost replay generations before restore", err)
	}
	if _, err = s.RestoreApp(ctx, app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	policy, err := s.(state.EnvironmentCachePolicyReader).GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || !policy.Inherited || policy.Revision != 0 {
		t.Fatal("purge mutated policy")
	}
	if _, err = store.GetEnvironmentCachePurge(ctx, uuid.NewString(), app.ID, "staging", req.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account=%v", err)
	}
	if err = s.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(replacement.ID, env.ID) {
		t.Fatal("environment UUID reused")
	}
	if _, err = store.GetEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recreated purge=%v", err)
	}
	if _, err = store.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", req); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("recreated replay=%v", err)
	}
}
