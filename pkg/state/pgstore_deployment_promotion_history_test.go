package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgListDeploymentPromotions(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, earlier := seedLiveDeploy(t, s, ctx, "live-promotion", "live-promotion")
	otherAccount, _, otherApp := seedLiveDeploy(t, s, ctx, "other-promotion", "other-promotion")
	before := time.Now().UTC().Truncate(time.Minute)
	checksum := strings.Repeat("a", 40)
	secondApp, err := s.CreateApp(ctx, state.App{AccountID: account, Slug: "same-account-promotion", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	create := func(appID, scope string) string {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{
			AppID: appID, Scope: scope, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:abc", Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	current, staging := create(app, api.DefaultEnvScope), create(app, "staging")
	boundary, deleted := create(app, api.DefaultEnvScope), create(app, api.DefaultEnvScope)
	secondAppDeployment := create(secondApp.ID, api.DefaultEnvScope)
	_ = create(app, api.DefaultEnvScope) // A deployment with no recorded promotion never appears.
	for _, seed := range []struct {
		id string
		at time.Time
	}{
		{earlier, before.Add(-20 * time.Minute)},
		{current, before.Add(-10 * time.Minute)},
		{staging, before.Add(-time.Minute)},
		{secondAppDeployment, before.Add(-time.Minute)},
		{otherApp, before.Add(-time.Minute)},
		{boundary, before},
		{deleted, before.Add(-time.Minute)},
	} {
		if _, err := pool.Exec(ctx, `update deployments set promoted_at = $2, commit_sha = $3 where id = $1`, seed.id, seed.at, checksum); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update deployments set deleted_at = $2 where id = $1`, deleted, before); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListDeploymentPromotions(ctx, account, app, "default", before, 10)
	if err != nil || len(got) != 2 || got[0].DeploymentID != current || got[0].CommitSHA != checksum ||
		!got[0].PromotedAt.Equal(before.Add(-10*time.Minute)) || got[1].DeploymentID != earlier {
		t.Fatalf("default scope: got %+v, err %v", got, err)
	}
	got, err = s.ListDeploymentPromotions(ctx, account, app, "default", before, 1)
	if err != nil || len(got) != 1 || got[0].DeploymentID != current {
		t.Fatalf("limit: got %+v, err %v", got, err)
	}
	got, err = s.ListDeploymentPromotions(ctx, otherAccount, app, "default", before, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("other account: got %+v, err %v", got, err)
	}
}
