package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type releaseInventoryStore interface {
	state.Store
	state.ProjectReleaseSetStore
	state.ProjectReleaseSetReader
}

func TestMemProjectReleaseReadContract(t *testing.T) {
	testProjectReleaseReadContract(t, state.NewMemStore())
}

func testProjectReleaseReadContract(t *testing.T, store releaseInventoryStore) (state.Account, state.Project, []state.ProjectReleaseSet) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "release-read-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "release-read", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "release-read-api", Status: state.AppActive, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:release-read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty active: %v", err)
	}
	empty, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", time.Time{}, "", 2)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty history: %+v %v", empty, err)
	}
	var releases []state.ProjectReleaseSet
	for i := 0; i < 3; i++ {
		release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	active, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || active.ID != releases[2].ID || !active.Active || active.EnvironmentSlug != "production" || len(active.Members) != 1 || active.Members[0].DeploymentID != dep.ID {
		t.Fatalf("active: %+v %v", active, err)
	}
	first, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", time.Time{}, "", 2)
	if err != nil || len(first) != 2 || first[0].ID != releases[2].ID || first[1].ID != releases[1].ID {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last := first[1]
	second, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", last.CreatedAt, last.ID, 2)
	if err != nil || len(second) != 1 || second[0].ID != releases[0].ID {
		t.Fatalf("second page: %+v %v", second, err)
	}
	retired, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", releases[0].ID)
	if err != nil || retired.Active || retired.ExpiresAt == nil {
		t.Fatalf("retired: %+v %v", retired, err)
	}
	retired.Members[0].DeploymentID = "mutated"
	*retired.ExpiresAt = time.Time{}
	retired, err = store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", releases[0].ID)
	if err != nil || retired.Members[0].DeploymentID != dep.ID || retired.ExpiresAt.IsZero() {
		t.Fatalf("read mutated stored graph: %+v %v", retired, err)
	}
	for _, tc := range []struct{ name, account, project, environment string }{
		{"foreign account", uuid.NewString(), project.ID, "production"},
		{"foreign project", account.ID, uuid.NewString(), "production"},
		{"wrong environment", account.ID, project.ID, "staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.ProjectReleaseSetByID(ctx, tc.account, tc.project, tc.environment, active.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("get: %v", err)
			}
			if _, err := store.ActiveProjectReleaseSet(ctx, tc.account, tc.project, tc.environment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("active: %v", err)
			}
			if _, err := store.ListProjectReleaseSetsBefore(ctx, tc.account, tc.project, tc.environment, time.Time{}, "", 2); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("list: %v", err)
			}
		})
	}
	if _, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", "invalid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid id: %v", err)
	}
	if _, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", time.Now(), "invalid", 2); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid cursor: %v", err)
	}
	return account, project, releases
}
