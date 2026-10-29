package imaged

// adr: 20261029103512 — an app layer over the plan cap keeps its problem code,
// and a Node source app pinned to a version without a shared runtime is told
// to declare Node 24.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodeVersionLayerFix(t *testing.T) {
	for _, tc := range []struct {
		spec, want string
	}{
		{"", ""},
		{"24", ""},
		{">=18", ""},
		{"22", "declares Node 22"},
		{"lts/jod", "declares Node 22"},
		{"^20", `declares Node "^20"`},
	} {
		got := nodeVersionLayerFix(tc.spec)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("nodeVersionLayerFix(%q) = %q, want it to contain %q", tc.spec, got, tc.want)
		}
	}
}

func writeNodeSource(t *testing.T, engines string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := `{"name":"big","engines":{"node":"` + engines + `"},"scripts":{"start":"node index.js"}}`
	if err := tw.WriteHeader(&tar.Header{Name: "package.json", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMarkDeployFailedKeepsAppLayerProblem(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("no Free plan limits")
	}
	for _, tc := range []struct {
		name, engines, wantFix string
	}{
		{"node 22 is told to declare node 24", "22", "declares Node 22"},
		{"undeclared gets the generic fix", "", "production dependencies only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "layer@example.com", api.PlanFree)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "big", Type: state.AppTypeApp})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: writeNodeSource(t, tc.engines)})
			if err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			problem := api.ErrAppLayerTooLarge(limits, 369<<20)
			h := &Handler{store: store}
			if err := h.markDeployFailed(ctx, dep.ID, fmt.Errorf("imaged: build local OCI app layer: %w", problem), "handleDeployment"); err != nil {
				t.Fatalf("markDeployFailed: %v", err)
			}
			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatalf("DeploymentByID: %v", err)
			}
			if got.ErrorCode != api.CodeAppLayerTooBig || got.Error != problem.Detail {
				t.Fatalf("error = %q (%s), want the problem's code and detail %q", got.Error, got.ErrorCode, problem.Detail)
			}
			if got.ErrorHint == "" || !strings.Contains(got.ErrorFix, tc.wantFix) {
				t.Fatalf("hint = %q fix = %q, want a fix containing %q", got.ErrorHint, got.ErrorFix, tc.wantFix)
			}
		})
	}
}
