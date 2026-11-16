package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// --readiness-path sets the primary app's steady-state readiness probe; it
// had only an API override, which source deploys dropped (2026-11-13).
func TestResolveDeployReadiness(t *testing.T) {
	probe, err := main.ResolveDeployReadiness("/ready", map[string]bool{"readiness-path": true})
	if err != nil || probe == nil || probe.Path != "/ready" {
		t.Fatalf("probe = %+v, err = %v", probe, err)
	}
	if probe, err := main.ResolveDeployReadiness("", nil); err != nil || probe != nil {
		t.Fatalf("unset flag = %+v, %v", probe, err)
	}
	for _, bad := range []string{"", "ready"} {
		if _, err := main.ResolveDeployReadiness(bad, map[string]bool{"readiness-path": true}); err == nil {
			t.Errorf("--readiness-path %q accepted", bad)
		}
	}
}

func TestDeployProbeOverridesKeepsBothProbes(t *testing.T) {
	startup := &api.DeploymentHealthcheck{Path: "/healthz"}
	readiness := &api.DeploymentReadinessProbe{Path: "/ready"}
	got := main.DeployProbeOverrides(startup, readiness)
	if got == nil || got.Healthcheck != startup || got.ReadinessProbe != readiness {
		t.Fatalf("overrides = %+v", got)
	}
	if got := main.DeployProbeOverrides(nil, nil); got != nil {
		t.Fatalf("no probes = %+v, want nil", got)
	}
	if got := main.DeployProbeOverrides(nil, readiness); got == nil || got.ReadinessProbe != readiness || got.Healthcheck != nil {
		t.Fatalf("readiness only = %+v", got)
	}
}

// A resumable upload session carries no readiness probe, so such a deploy
// uses multipart, as one with a liveness probe does.
func TestReadinessProbeUsesMultipartUpload(t *testing.T) {
	plain := api.DeployAnnotations{}
	if !main.CanUseResumableUpload(main.ShapeApp, "", "", false, "", plain, -1, "", "") {
		t.Fatal("a plain app deploy should use the resumable upload")
	}
	withReadiness := api.DeployAnnotations{ReadinessProbe: &api.DeploymentReadinessProbe{Path: "/ready"}}
	if main.CanUseResumableUpload(main.ShapeApp, "", "", false, "", withReadiness, -1, "", "") {
		t.Fatal("a deploy with a readiness probe took the resumable upload, which would drop it")
	}
}
