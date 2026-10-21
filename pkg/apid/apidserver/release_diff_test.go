package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021101117 — the release diff compares with the release that
// served before, by config key, never by value.
func TestReleaseDiffComparesWithTheReleaseThatServedBefore(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "diff-app"})
	if err != nil {
		t.Fatal(err)
	}
	setEnv := func(key, value string) {
		t.Helper()
		if rec := e.Do(t, http.MethodPut, "/v1/apps/diff-app/env/"+key, api.PutAppEnvRequest{Value: value}, nil); rec.Code >= 300 {
			t.Fatalf("set %s = %d %s", key, rec.Code, rec.Body)
		}
	}
	deploy := func(digest, commit string, at time.Time, live bool) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: digest, CommitSHA: commit,
			SourceURL: "https://github.com/acme/shop", Handler: "index.handler", ReleaseCommand: []string{"node", "migrate.mjs"}, CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		if live {
			if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	setEnv("KEEP", "kept-value")
	setEnv("EDIT", "first-value")
	served := deploy("sha256:served", strings.Repeat("a", 40), time.Now().UTC(), true)
	time.Sleep(10 * time.Millisecond)
	setEnv("EDIT", "second-value")
	setEnv("NEW", "new-value")
	failed := deploy("sha256:failed", strings.Repeat("b", 40), time.Now().UTC().Add(time.Second), false)
	if err := store.UpdateDeploymentStatus(ctx, failed.ID, state.DeployFailed, "build failed"); err != nil {
		t.Fatal(err)
	}
	candidate := deploy("sha256:candidate", strings.Repeat("c", 40), time.Now().UTC().Add(2*time.Second), false)

	var diff api.ReleaseDiff
	rec := guardDo(t, e, http.MethodGet, "/v1/deployments/"+candidate.ID+"/diff", nil, &diff)
	if rec.Code != http.StatusOK || diff.From == nil || diff.From.DeploymentID != served.ID {
		t.Fatalf("diff = %d %s", rec.Code, rec.Body)
	}
	if !diff.Code.Changed || !diff.Code.ImageChanged || !strings.HasPrefix(diff.Code.CompareURL, "https://github.com/acme/shop/compare/") {
		t.Fatalf("code = %+v", diff.Code)
	}
	keys := map[string]string{}
	for _, k := range diff.Config.Variables {
		keys[k.Key] = k.Change
	}
	if !diff.Config.Known || len(keys) != 2 || keys["EDIT"] != api.ReleaseDiffChanged || keys["NEW"] != api.ReleaseDiffAdded {
		t.Fatalf("config = %+v", diff.Config)
	}
	for _, value := range []string{"kept-value", "first-value", "second-value", "new-value"} {
		if strings.Contains(rec.Body.String(), value) {
			t.Fatalf("diff leaked %q: %s", value, rec.Body)
		}
	}
	if diff.Dependencies.Known || diff.Dependencies.Note == "" || diff.Database.Changed || !strings.Contains(diff.Summary, "2 config keys") {
		t.Fatalf("diff = %+v", diff)
	}

	// Compare with an explicit release; a release of another app is not found.
	rec = guardDo(t, e, http.MethodGet, "/v1/deployments/"+candidate.ID+"/diff?against="+failed.ID, nil, &diff)
	if rec.Code != http.StatusOK || diff.From == nil || diff.From.DeploymentID != failed.ID {
		t.Fatalf("against = %d %s", rec.Code, rec.Body)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/deployments/"+candidate.ID+"/diff?against="+candidate.ID, nil, nil),
		http.StatusBadRequest, api.CodeValidation)
	other, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "diff-other"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, ImageDigest: "sha256:foreign"})
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/deployments/"+candidate.ID+"/diff?against="+foreign.ID, nil, nil),
		http.StatusNotFound, api.CodeNotFound)

	// The app's first release has nothing to compare with.
	var first api.ReleaseDiff
	rec = guardDo(t, e, http.MethodGet, "/v1/deployments/"+served.ID+"/diff", nil, &first)
	if rec.Code != http.StatusOK || first.From != nil || first.Summary != "first release" {
		t.Fatalf("first release = %d %s", rec.Code, rec.Body)
	}
}
