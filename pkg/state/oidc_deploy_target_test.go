package state_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type oidcDeployTargetStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	UpsertGithubInstallBinding(ctx context.Context, b state.GitHubBinding) error
	state.OIDCDeployTargetResolver
}

const githubActionsIssuer = "https://token.actions.githubusercontent.com"

// A GitHub Actions identity minted under a trust policy Gregale created may
// deploy an app bound to its repository, from the branches Gregale deploys
// that repository from (ADR-20261101100000).
func exerciseOIDCDeployTarget(t *testing.T, store oidcDeployTargetStore) {
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	acct, err := store.CreateAccount(ctx, "oidc-target-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(slug, repo, branch string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug + "-" + suffix, Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: app.ID, AccountID: acct.ID,
			BindingID: "bind-" + app.ID, InstallID: 7, RepoFullName: repo, ProductionBranch: branch}); err != nil {
			t.Fatal(err)
		}
		return app
	}
	web := bind("web", "octocat/hello-"+suffix, "main")
	bind("api", "octocat/hello-"+suffix, "release")
	billing := bind("billing", "octocat/other-"+suffix, "main")
	subject := "repo:octocat/hello-" + suffix + ":ref:refs/heads/feature-7"

	target, err := store.OIDCDeployTargetForSubject(ctx, acct.ID, githubActionsIssuer, subject, web.Slug)
	if err != nil || target.AppID != web.ID || !slices.Equal(target.Branches, []string{"main", "release"}) {
		t.Fatalf("target = (%+v, %v), want web with the repository's production branches", target, err)
	}
	if _, err := store.OIDCDeployTargetForSubject(ctx, acct.ID, githubActionsIssuer, subject, billing.Slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another repository's app = %v, want ErrNotFound", err)
	}
	if _, err := store.OIDCDeployTargetForSubject(ctx, acct.ID, "https://idp.example.com", subject, web.Slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("non-GitHub issuer = %v, want ErrNotFound", err)
	}
}

func TestMemStoreOIDCDeployTarget(t *testing.T) {
	exerciseOIDCDeployTarget(t, state.NewMemStore())
}

func TestPgStoreOIDCDeployTarget(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseOIDCDeployTarget(t, state.NewPgStore(pool))
}
