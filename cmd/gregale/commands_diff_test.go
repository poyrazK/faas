package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

func TestNormalizeDeployPreviewFlags(t *testing.T) {
	tests := []struct {
		name    string
		dryRun  bool
		diff    bool
		want    bool
		wantErr error
	}{
		{name: "neither", want: false},
		{name: "diff compatibility", diff: true, want: true},
		{name: "dry run", dryRun: true, want: true},
		{name: "aliases are mutually exclusive", dryRun: true, diff: true, wantErr: errors.New("--dry-run and --diff are aliases; use only one")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDeployPreviewFlags(tt.dryRun, tt.diff)
			if got != tt.want {
				t.Fatalf("normalizeDeployPreviewFlags() = %t, want %t", got, tt.want)
			}
			if (err == nil) != (tt.wantErr == nil) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && err.Error() != tt.wantErr.Error() {
				t.Fatalf("error = %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDeployDiffManifest_RejectsWorkflows(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `workflows:
  - name: process_order
    steps:
      - name: charge
        run: charge_stripe
`)

	err := validateDeployDiffManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "not supported by deploy --diff") {
		t.Fatalf("error = %v, want explicit workflow diff error", err)
	}
}

func TestBuildPreviewBuildPlan_PreservesResolvedSourceIntent(t *testing.T) {
	got := buildPreviewBuildPlan("", shapeFunction, "python312", "handler.handler", "abc", false)
	if got.Class != "function" || got.Framework != "python" || got.Runtime != "python312" || got.Handler != "handler.handler" || got.SourceSHA256 != "abc" {
		t.Fatalf("function preview plan = %+v", got)
	}

	got = buildPreviewBuildPlan("", shapeApp, "", "", "", true)
	if got.Class != "app" || got.Framework != "unknown" {
		t.Fatalf("image preview plan = %+v, want app/unknown", got)
	}
}

func TestDiffRequestFromCLI_CarriesBuildPlan(t *testing.T) {
	want := &api.BuildPlan{Framework: "node", Runtime: "node22", Class: "app", SourceSHA256: "abc"}
	req := diffRequestFromCLI(diffCLIOptions{BuildPlan: want})
	if req.BuildPlan != want {
		t.Fatalf("build_plan pointer was not carried through: got=%p want=%p", req.BuildPlan, want)
	}
}

func TestDiffRequestFromCLI_PreservesResourceAndProtocolPatch(t *testing.T) {
	ram, cpu := 512, 750
	protocol := "grpc"
	req := diffRequestFromCLI(diffCLIOptions{AppConfig: deploydiff.AppConfigPatch{
		RAMMB: &ram, CPUMillicores: &cpu, AppProtocol: &protocol,
	}})
	if req.AppConfig == nil {
		t.Fatal("app_config = nil, want projected patch")
	}
	if req.AppConfig.RAMMB == nil || *req.AppConfig.RAMMB != ram {
		t.Fatalf("ram_mb = %v, want %d", req.AppConfig.RAMMB, ram)
	}
	if req.AppConfig.CPUMillicores == nil || *req.AppConfig.CPUMillicores != cpu {
		t.Fatalf("cpu_millicores = %v, want %d", req.AppConfig.CPUMillicores, cpu)
	}
	if req.AppConfig.AppProtocol == nil || *req.AppConfig.AppProtocol != protocol {
		t.Fatalf("app_protocol = %v, want %q", req.AppConfig.AppProtocol, protocol)
	}
}

func TestBuildPending_CarriesBuildPlan(t *testing.T) {
	want := &api.BuildPlan{Framework: "python", Runtime: "python312", Class: "function", Handler: "handler.handler"}
	pending := buildPending(nil, nil, diffCLIOptions{Slug: "fresh", BuildPlan: want}, deploydiff.EmptyBaseline())
	if pending.BuildPlan != want {
		t.Fatalf("build_plan pointer was not carried into local pending projection: got=%p want=%p", pending.BuildPlan, want)
	}
}

func TestPreviewCronsFromManifest_IsSharedByPreviewModes(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `triggers:
  - kind: cron
    app: fresh
    schedule: "0 3 * * *"
    path: /cleanup
  - kind: cron
    app: another
    schedule: "0 4 * * *"
    path: /ignore
`)

	crons := previewCronsFromManifest(dir, "fresh")
	if len(crons) != 1 {
		t.Fatalf("cron projection length = %d, want 1: %+v", len(crons), crons)
	}
	if crons[0].Schedule != "0 3 * * *" || crons[0].Path != "/cleanup" {
		t.Fatalf("cron projection = %+v, want selected app trigger", crons[0])
	}
	if crons[0].Enabled == nil || !*crons[0].Enabled {
		t.Fatalf("cron enabled = %v, want true", crons[0].Enabled)
	}

	opts := diffCLIOptions{Cwd: dir, Slug: "fresh"}
	pending := buildPending(nil, nil, opts, deploydiff.EmptyBaseline())
	if len(pending.Crons) != len(crons) || pending.Crons[0].Path != crons[0].Path {
		t.Fatalf("local pending crons = %+v, want server request projection %+v", pending.Crons, crons)
	}
}
