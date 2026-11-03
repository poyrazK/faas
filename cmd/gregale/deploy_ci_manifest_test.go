// adr: 20261031162858 amendment 1 — a deploy credential deploys a gregale.yaml.

package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// deployOnlyAPI answers like apid for a credential that carries only
// deploy:write: the routes in refused answer 403 insufficient_scope, GET of
// the app answers, anything else 404.
func deployOnlyAPI(t *testing.T, refused ...string) *main.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, path := range refused {
			if r.URL.Path == path {
				w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "this endpoint requires one of: admin,apps:read"))
				return
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api" {
			main.WriteJSONTestStatus(w, http.StatusOK, api.AppResponse{ID: "app-id", Slug: "api"})
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled"))
	}))
	t.Cleanup(srv.Close)
	return main.NewClient(srv.URL, "fp_deploy_test")
}

// A gregale.yaml with workflows made the deploy read the account's plan, a
// read a deploy token or the deploy Action's OIDC bearer is refused, so the
// deploy failed before it uploaded. apid checks workflows against the plan
// when it creates the deployment; the CLI's early check is skipped.
func TestWorkflowManifestDeploysWithoutAccountRead(t *testing.T) {
	dir := t.TempDir()
	main.WriteManifest(t, dir, "workflows:\n  - name: nightly\n    steps:\n      - name: run\n        run: api\n")
	client := deployOnlyAPI(t, "/v1/account")
	workflows, err := main.LoadWorkflowManifestForDeploy(context.Background(), client, dir)
	if err != nil || len(workflows) != 1 || workflows[0].Name != "nightly" {
		t.Fatalf("workflows = %+v, err %v; want the manifest's workflow without the plan read", workflows, err)
	}
}

// Attaching a database or binding a bucket grants the app a credential, so
// it keeps postgres:manage or storage:manage. A deploy credential is refused
// with the scope it needs, not the read scope of the listing it failed.
func TestManifestBindingsNameTheScopeADeployCredentialLacks(t *testing.T) {
	t.Run("databases", func(t *testing.T) {
		dir := t.TempDir()
		main.WriteManifest(t, dir, "databases:\n  - database: orders\n")
		err := main.DeployManifestPostgresBindings(context.Background(), deployOnlyAPI(t, "/v1/postgres/databases"), "api", dir)
		if err == nil || !strings.Contains(err.Error(), "postgres:manage") {
			t.Fatalf("err = %v; want a refusal naming postgres:manage", err)
		}
	})
	t.Run("buckets", func(t *testing.T) {
		dir := t.TempDir()
		main.WriteManifest(t, dir, "buckets:\n  - bucket: assets\n    app: api\n    scope: production\n")
		err := main.DeployManifestObjectStorageBindings(context.Background(), deployOnlyAPI(t, "/v1/apps/api/buckets"), "api", dir)
		if err == nil || !strings.Contains(err.Error(), "storage:manage") {
			t.Fatalf("err = %v; want a refusal naming storage:manage", err)
		}
	})
}

// A source deploy whose credential may not read the plan used the Free
// floor (100 MB), refusing a Pro or Scale source apid would accept.
func TestLargestSourceTarballMBCoversEveryPlan(t *testing.T) {
	largest := main.LargestSourceTarballMB()
	for _, plan := range api.Plans {
		if limits := api.MustLimitsFor(plan); limits.SourceTarballMaxMB > largest {
			t.Fatalf("%s accepts %d MB, above the %d MB a deploy credential packs", plan, limits.SourceTarballMaxMB, largest)
		}
	}
}
