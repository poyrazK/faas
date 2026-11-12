// adr: 20261031193635
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 6c: a contained branch of an environment with object storage is
// admitted only behind the bridge object relay gate, and its bucket binding
// names the relay; a live branch keeps the public endpoint.
func TestContainedEnvironmentBranchBindsBucketsThroughRelay(t *testing.T) {
	e := apidserver.SetupSecrets(t, api.PlanPro)
	// After SetupSecrets, which installs a recipient of its own.
	identity, restore := apidserver.WithTestIdentities(t)
	defer restore()
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	for _, key := range []string{apidserver.RuntimeConfigS3, apidserver.RuntimeConfigEnvironmentBranches} {
		if err := (*srv.RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
	}
	*srv.EnvironmentBranchContainmentEnabled() = true
	srv.WithObjectStorage(apidserver.ObjectRegistry(t, newStorageTestObjectProvider(map[string][]byte{}, nil), newStorageTestObjectProvider(nil, nil), "external"))
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Do(t, http.MethodPost, "/v1/apps/shop-web/buckets", map[string]any{"name": "assets", "scope": "production"}, nil)
	var source api.ObjectBucket
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &source) != nil {
		t.Fatalf("create bucket = %d %s", created.Code, created.Body.String())
	}
	bound := e.Do(t, http.MethodPost, "/v1/apps/shop-web/buckets/"+source.ID+"/compute-bindings", api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite}, nil)
	var parentBinding api.ObjectStorageComputeBinding
	if bound.Code != http.StatusCreated || json.Unmarshal(bound.Body.Bytes(), &parentBinding) != nil {
		t.Fatalf("bind bucket = %d %s", bound.Code, bound.Body.String())
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	branch := func(slug, egress string) (int, string) {
		t.Helper()
		body, _ := json.Marshal(api.CreateProjectEnvironmentBranchRequest{Slug: slug, Egress: egress})
		req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/branches", "shop", body)
		req.SetPathValue("environment", "production")
		srv.CreateProjectEnvironmentBranch(rec, req, acct)
		return rec.Code, rec.Body.String()
	}
	if code, body := branch("pr-c", api.EnvironmentBranchEgressContained); code != http.StatusConflict || !json.Valid([]byte(body)) ||
		!containsCode(body, api.CodeEnvironmentBranchContainedObjects) {
		t.Fatalf("contained branch with the relay off = %d %s", code, body)
	}
	*srv.EnvironmentBranchContainedObjectsEnabled() = true
	if code, body := branch("pr-c", api.EnvironmentBranchEgressContained); code != http.StatusCreated {
		t.Fatalf("contained branch with the relay on = %d %s", code, body)
	}
	if code, body := branch("pr-l", api.EnvironmentBranchEgressLive); code != http.StatusCreated {
		t.Fatalf("live branch = %d %s", code, body)
	}
	for range 2 {
		if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	keys := object.ObjectStorageBindingSecretKeys(parentBinding.Prefix)
	endpoint := func(scope string) string {
		t.Helper()
		secret, err := store.GetAppSecretInScope(ctx, acct.ID, app.ID, scope, keys.Endpoint)
		if err != nil {
			t.Fatalf("%s endpoint secret: %v", scope, err)
		}
		envelope, _, err := secretbox.OpenMultiBound([]*age.X25519Identity{identity}, secret.Ciphertext)
		if err != nil {
			t.Fatalf("open %s endpoint: %v", scope, err)
		}
		return envelope[keys.Endpoint]
	}
	if got := endpoint("pr-c"); got != api.EnvironmentBranchObjectRelayEndpoint() {
		t.Fatalf("contained branch endpoint = %q, want the relay %q", got, api.EnvironmentBranchObjectRelayEndpoint())
	}
	if got := endpoint("pr-l"); got == "" || got == api.EnvironmentBranchObjectRelayEndpoint() {
		t.Fatalf("live branch endpoint = %q, want the public endpoint", got)
	}
}

func containsCode(body, code string) bool {
	var problem struct {
		Code string `json:"code"`
	}
	return json.Unmarshal([]byte(body), &problem) == nil && problem.Code == code
}
