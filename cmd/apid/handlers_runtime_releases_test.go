package main

// adr: 736

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func apiRuntimeRelease(runtime, arch, digit string) state.RuntimeRelease {
	r := state.RuntimeRelease{Runtime: runtime, Architecture: arch, SourceRef: "ghcr.io/private/operator@sha256:" + strings.Repeat(digit, 64), GuestInitSHA256: strings.Repeat("a", 64), LayoutVersion: "test-layout", BaseSHA256: strings.Repeat(digit, 64)}
	r.ID = r.Identity()
	return r
}
func TestDeploymentRuntimeEvidenceAndReadOnlyPreview(t *testing.T) {
	e := setup(t, api.PlanFree)
	ctx := t.Context()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "runtime-evidence", Type: state.AppTypeFunction, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployImaging})
	if err != nil {
		t.Fatal(err)
	}
	key := "apps/runtime-evidence/layer.ext4"
	if err := e.store.SetDeploymentRootfs(ctx, dep.ID, "/test/layer", key, 20); err != nil {
		t.Fatal(err)
	}
	releases := e.store
	current, err := releases.PublishRuntimeRelease(ctx, apiRuntimeRelease("node22", "amd64", "1"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := releases.PublishRuntimeRelease(ctx, apiRuntimeRelease("node22", "amd64", "2"))
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + dep.ID + "/runtime"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var unknown api.DeploymentRuntimeResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &unknown) != nil || unknown.Status != "unknown" || unknown.Current != nil {
		t.Fatal("guessed provenance", rec.Code, rec.Body)
	}
	if err := releases.BindDeploymentRuntimeRelease(ctx, dep.ID, key, current.ID); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, path, nil, nil)
	var evidence api.DeploymentRuntimeResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &evidence) != nil || evidence.Status != "pinned" || evidence.Current.ID != current.ID || len(evidence.Releases) != 2 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "ghcr.io/private") {
		t.Fatal("exposed operator repository")
	}
	rec = e.do(t, http.MethodGet, path+"/upgrade-preview?target="+target.ID, nil, nil)
	var preview api.RuntimeUpgradePreviewResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &preview) != nil || preview.Disposition != "review_required" || !preview.RebuildRequired || !preview.ColdStartRequired || preview.ExecutionAvailable || len(preview.Blockers) != 2 {
		t.Fatal(rec.Code, rec.Body)
	}
	if got, err := releases.RuntimeReleaseForArtifact(ctx, e.acct.ID, key); err != nil || got.ID != current.ID {
		t.Fatal("preview changed runtime", got, err)
	}
	if after, err := e.store.DeploymentByID(ctx, dep.ID); err != nil || after.Status != dep.Status || after.TrafficPercent != dep.TrafficPercent {
		t.Fatal("preview changed deployment", after, err)
	}
	for _, query := range []string{"", "?target=latest", "?target=" + strings.Repeat("f", 64)} {
		want := 400
		if len(query) > 40 {
			want = 404
		}
		if rec := e.do(t, http.MethodGet, path+"/upgrade-preview"+query, nil, nil); rec.Code != want {
			t.Fatal(query, rec.Code, rec.Body)
		}
	}
	image, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	imageRead := e.do(t, http.MethodGet, "/v1/deployments/"+image.ID+"/runtime", nil, nil)
	var unsupported api.DeploymentRuntimeResponse
	if imageRead.Code != 200 || json.Unmarshal(imageRead.Body.Bytes(), &unsupported) != nil || unsupported.Status != "unsupported" || unsupported.Current != nil {
		t.Fatal("claimed managed customer image", imageRead.Body)
	}
	foreign, err := e.store.CreateAccount(ctx, "foreign-runtime@test.example", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.store.CreateApp(ctx, state.App{AccountID: foreign.ID, Slug: "foreign-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	otherDep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/runtime", "/runtime/upgrade-preview?target=" + target.ID} {
		if rec := e.do(t, http.MethodGet, "/v1/deployments/"+otherDep.ID+suffix, nil, nil); rec.Code != 404 {
			t.Fatal("foreign deployment", rec.Code)
		}
	}
}
func TestRuntimeUpgradePreviewNeverInfersCompatibility(t *testing.T) {
	current := runtimeReleaseResponse(apiRuntimeRelease("node22", "amd64", "1"))
	for _, tc := range []struct {
		name    string
		current *api.RuntimeReleaseResponse
		target  api.RuntimeReleaseResponse
		want    string
	}{
		{"unknown", nil, current, "blocked"},
		{"same", &current, current, "no_change"},
		{"family", &current, runtimeReleaseResponse(apiRuntimeRelease("node24", "amd64", "2")), "blocked"},
		{"architecture", &current, runtimeReleaseResponse(apiRuntimeRelease("node22", "arm64", "2")), "blocked"},
		{"unqualified publication", &current, runtimeReleaseResponse(apiRuntimeRelease("node22", "amd64", "2")), "review_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runtimeUpgradePreview(api.DeploymentRuntimeResponse{Current: tc.current, Reason: "Unknown base"}, tc.target)
			if out.Disposition != tc.want || out.ExecutionAvailable {
				t.Fatal(out)
			}
		})
	}
}
