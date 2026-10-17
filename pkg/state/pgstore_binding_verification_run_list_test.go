//go:build !no_pg

package state_test

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"testing"
	"time"
)

func TestPgVerificationRunListsKeysetFiltersAndIsolation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, e := s.CreateAccount(ctx, "receipt-list@example.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	app, e := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "receipt-list", Type: state.AppTypeFunction})
	if e != nil {
		t.Fatal(e)
	}
	dep, e := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:list"})
	if e != nil {
		t.Fatal(e)
	}
	project, e := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "receipt-list"})
	if e != nil {
		t.Fatal(e)
	}
	pgid := func(id string) pgtype.UUID {
		var value pgtype.UUID
		if err := value.Scan(id); err != nil {
			t.Fatal(err)
		}
		return value
	}
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	stamp := pgtype.Timestamptz{Time: created, Valid: true}
	expiry := pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true}
	q := sqlc.New()
	graphID := uuid.NewString()
	snapshot, _ := json.Marshal(map[string]any{"Report": api.ProjectBindingVerificationRun{Manifest: api.CheckedProjectReleaseManifest{Deployments: map[string]string{"receipt-list": dep.ID}}}})
	e = q.InsertProjectBindingVerificationRun(ctx, pool, sqlc.InsertProjectBindingVerificationRunParams{ID: pgid(graphID), AccountID: pgid(acct.ID), ProjectID: pgid(project.ID), Environment: "production", Snapshot: snapshot, CreatedAt: stamp, ExpiresAt: expiry})
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", i)
		e = q.InsertBindingVerificationRun(ctx, pool, sqlc.InsertBindingVerificationRunParams{ID: pgid(id), AccountID: pgid(acct.ID), AppID: pgid(app.ID), DeploymentID: pgid(dep.ID), Scope: "production", Snapshot: []byte(`{"private":"never returned"}`), CreatedAt: stamp, ExpiresAt: expiry})
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 {
			_, e = q.LinkProjectBindingVerificationMember(ctx, pool, sqlc.LinkProjectBindingVerificationMemberParams{ID: pgid(id), GraphRunID: pgid(graphID)})
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	options := api.BindingVerificationRunListOptions{Limit: 2, DeploymentID: dep.ID, Scope: "production", Status: "expired"}
	first, e := s.ListBindingVerificationRuns(ctx, acct.ID, app.ID, options)
	if e != nil || len(first.Runs) != 2 || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, e)
	}
	options.Cursor = first.NextCursor
	second, e := s.ListBindingVerificationRuns(ctx, acct.ID, app.ID, options)
	if e != nil || len(second.Runs) != 2 || second.NextCursor != "" || !second.ObservedAt.Equal(first.ObservedAt) {
		t.Fatalf("second: %+v %v", second, e)
	}
	if first.Runs[0].ID != "11111111-1111-4111-8111-000000000005" || second.Runs[1].ID != "11111111-1111-4111-8111-000000000002" {
		t.Fatal("unstable tied ordering")
	}
	foreign, e := s.ListBindingVerificationRuns(ctx, uuid.NewString(), app.ID, api.BindingVerificationRunListOptions{})
	if e != nil || len(foreign.Runs) != 0 {
		t.Fatal("account leaked", e)
	}
	graph, e := s.ListProjectBindingVerificationRuns(ctx, acct.ID, project.ID, "production", api.BindingVerificationRunListOptions{DeploymentID: dep.ID, Status: "expired"})
	if e != nil || len(graph.Runs) != 1 || graph.Runs[0].ID != graphID {
		t.Fatalf("graph snapshot filter: %+v %v", graph, e)
	}
	absent, e := s.ListProjectBindingVerificationRuns(ctx, acct.ID, project.ID, "other", api.BindingVerificationRunListOptions{})
	if e != nil || len(absent.Runs) != 0 {
		t.Fatal("environment leaked", e)
	}
	options = api.BindingVerificationRunListOptions{IncludeMembers: true}
	all, e := s.ListBindingVerificationRuns(ctx, acct.ID, app.ID, options)
	if e != nil || len(all.Runs) != 5 || all.Runs[4].GraphRunID != graphID {
		t.Fatalf("member linkage %+v %v", all, e)
	}
}
