// adr: 375
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type serviceSnapshotStoreFixture struct {
	reader state.ServicePolicyReader
	err    error
}

func (s serviceSnapshotStoreFixture) WithServicePolicySnapshot(_ context.Context, read func(state.ServicePolicyReader) error) error {
	if s.err != nil {
		return s.err
	}
	return read(s.reader)
}

func TestServicePolicyPinnerPreservesDenialsAndExcludesUnrelatedManifest(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "snapshot@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "snapshot-caller", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "snapshot-target", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	pin := newServicePolicyPinner(serviceSnapshotStoreFixture{reader: store})
	before, err := pin(t.Context(), caller.ID, "snapshot-target", false)
	if err != nil || !before.Found || before.AuthorizationError != nil {
		t.Fatalf("before = %+v %v", before, err)
	}
	caller.Manifest.Env = map[string]string{"SECRET": "must-never-enter-proof"}
	if _, err := store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
	after, err := pin(t.Context(), caller.ID, "snapshot-target", false)
	if err != nil || after.InputRevision != before.InputRevision {
		t.Fatalf("unrelated environment changed proof: %+v %v", after, err)
	}
	caller.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
	if _, err := store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
	denied, err := pin(t.Context(), caller.ID, "snapshot-target", false)
	if err != nil || !errors.Is(denied.AuthorizationError, gateway.ErrServiceProxyBindingDenied) || denied.InputRevision == before.InputRevision {
		t.Fatalf("binding denial = %+v %v", denied, err)
	}
	alias, err := pin(t.Context(), caller.ID, "snapshot-target", true)
	if err != nil || alias.AliasAllowed || alias.Found || alias.InputRevision == "" {
		t.Fatalf("alias denial = %+v %v", alias, err)
	}
	if _, err := newServicePolicyPinner(serviceSnapshotStoreFixture{err: errors.New("offline")})(t.Context(), caller.ID, "snapshot-target", false); err == nil {
		t.Fatal("store failure became verified policy")
	}
}

type serviceSnapshotAfterRead struct {
	state.ServicePolicySnapshotStore
	after func()
}

func (s serviceSnapshotAfterRead) WithServicePolicySnapshot(ctx context.Context, read func(state.ServicePolicyReader) error) error {
	return s.ServicePolicySnapshotStore.WithServicePolicySnapshot(ctx, func(reader state.ServicePolicyReader) error {
		return read(&serviceSnapshotAfterReadReader{ServicePolicyReader: reader, after: s.after})
	})
}

type serviceSnapshotAfterReadReader struct {
	state.ServicePolicyReader
	after func()
}

func (s *serviceSnapshotAfterReadReader) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.ServicePolicyReader.AppByID(ctx, id)
	if s.after != nil && err == nil {
		after := s.after
		s.after = nil
		after()
	}
	return app, err
}

func TestServicePolicyPinnerPostgresUsesOneCommittedView(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "service-snapshot-pg@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128,
			Manifest: state.AppManifest{Env: map[string]string{"SECRET": "excluded"}}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := create("service-snapshot-caller"), create("service-snapshot-target")
	before, err := newServicePolicyPinner(store)(t.Context(), caller.ID, target.Slug, false)
	if err != nil {
		t.Fatal(err)
	}
	var changed bool
	pin := newServicePolicyPinner(serviceSnapshotAfterRead{ServicePolicySnapshotStore: store, after: func() {
		changeServiceSnapshotPolicy(t, pool, caller.ID, target.ID)
		changed = true
	}})
	during, err := pin(t.Context(), caller.ID, target.Slug, false)
	if err != nil || !changed || during.AuthorizationError != nil || during.Target.AppProtocol != api.AppProtocolHTTP1 || during.InputRevision != before.InputRevision {
		t.Fatalf("mixed policy read: changed=%v snapshot=%+v err=%v", changed, during, err)
	}
	fresh, err := newServicePolicyPinner(store)(t.Context(), caller.ID, target.Slug, false)
	if err != nil || !errors.Is(fresh.AuthorizationError, gateway.ErrServiceProxyBindingDenied) || fresh.Target.AppProtocol != api.AppProtocolGRPC || fresh.InputRevision == before.InputRevision {
		t.Fatalf("fresh policy read = %+v %v", fresh, err)
	}
	if err := store.WithServicePolicySnapshot(t.Context(), func(reader state.ServicePolicyReader) error {
		app, err := reader.AppByID(t.Context(), target.ID)
		if err != nil {
			return err
		}
		if len(app.Manifest.Env) != 0 {
			t.Fatal("snapshot projection included credentials")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("snapshot retained a pool connection after completion")
	}
	if err := store.WithServicePolicySnapshot(t.Context(), func(state.ServicePolicyReader) error { return errors.New("callback failed") }); err == nil {
		t.Fatal("callback error lost")
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("failed snapshot retained its transaction")
	}
}

func changeServiceSnapshotPolicy(t *testing.T, pool *pgxpool.Pool, caller, target string) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE apps SET manifest=manifest || '{"service_binding_policy":"declared"}'::jsonb WHERE id=$1`, caller); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE apps SET app_protocol='grpc' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServicePolicyPinnerPostgresScopedNamespaces(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "snapshot-scope@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "snapshot-scope"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	create := func(slug, parent, workload string, pr int) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug,
			Type: state.AppTypeApp, RAMMB: 128, WorkloadName: workload, PreviewOfSlug: parent, PreviewPrNumber: pr,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	prod := create("snapshot-billing", "", "billing", 0)
	caller := create("snapshot-pr-client", "client", "client", 42)
	preview := create("snapshot-pr-billing", prod.Slug, "billing", 42)
	pin := newServicePolicyPinner(store)
	sibling, err := pin(t.Context(), caller.ID, "billing", false)
	if err != nil || sibling.Target.AppID != preview.ID || !sibling.Target.PreviewScoped || sibling.AuthorizationError != nil {
		t.Fatalf("PR sibling = %+v %v", sibling, err)
	}
	productionDenied, err := pin(t.Context(), caller.ID, prod.Slug, false)
	if err != nil || !errors.Is(productionDenied.AuthorizationError, gateway.ErrServiceProxyPreviewProductionDenied) {
		t.Fatalf("PR production fallback = %+v %v", productionDenied, err)
	}
	policy := state.DefaultGitHubDeployPolicy(project.ID, account.ID)
	policy.PreviewServicePolicy = state.PreviewServicePolicyAllowMarked
	if _, err := store.UpsertGitHubDeployPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	productionAllowed, err := pin(t.Context(), caller.ID, prod.Slug, false)
	if err != nil || productionAllowed.AuthorizationError != nil || productionAllowed.InputRevision == productionDenied.InputRevision {
		t.Fatalf("project policy change = %+v %v", productionAllowed, err)
	}
	testCaller := create("snapshot-test-client", "client", "client", 0)
	testTarget := create("snapshot-test-billing", prod.Slug, "billing", 0)
	const run = "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(t.Context(), account.ID, run, []state.ScenarioTestMember{
		{AppID: testCaller.ID, Workload: "client"}, {AppID: testTarget.ID, Workload: "billing"},
	}); err != nil {
		t.Fatal(err)
	}
	testSibling, err := pin(t.Context(), testCaller.ID, "billing", false)
	if err != nil || testSibling.Target.AppID != testTarget.ID || testSibling.AuthorizationError != nil {
		t.Fatalf("test sibling = %+v %v", testSibling, err)
	}
	noFallback, err := pin(t.Context(), testCaller.ID, prod.Slug, false)
	if err != nil || noFallback.Found {
		t.Fatalf("test reached production fallback: %+v %v", noFallback, err)
	}
}
