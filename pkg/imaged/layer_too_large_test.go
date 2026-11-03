package imaged

// adr: 20261029103512 — an app layer over the plan cap keeps its problem code,
// and a Node or Python source app pinned to a version without a shared
// runtime is told to declare one that has it (Python: ADR-1500 amendment 2).

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

// ADR-1500 amendment 2: a Python app pinned to a version without a shared
// runtime is told to declare 3.12 or 3.13.
func TestPythonVersionLayerFix(t *testing.T) {
	for _, tc := range []struct {
		specs []string
		want  string
	}{
		{nil, ""},
		{[]string{"3.12"}, ""},
		{[]string{"python-3.13.1"}, ""},
		{[]string{"3.11"}, `declares Python "3.11"`},
		{[]string{"3.12", "3.13"}, `declares Python "3.12", "3.13"`},
	} {
		got := pythonVersionLayerFix(tc.specs)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("pythonVersionLayerFix(%q) = %q, want it to contain %q", tc.specs, got, tc.want)
		}
	}
}

func writeNodeSource(t *testing.T, engines string) string {
	t.Helper()
	return writeSource(t, map[string]string{"package.json": `{"name":"big","engines":{"node":"` + engines + `"},"scripts":{"start":"node index.js"}}`})
}

func writeSource(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
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
	python311 := map[string]string{"requirements.txt": "flask\n", "app.py": "print('hi')\n", ".python-version": "3.11\n"}
	for _, tc := range []struct {
		name, engines, wantFix string
		files                  map[string]string
	}{
		{"node 22 is told to declare node 24", "22", "declares Node 22", nil},
		{"undeclared gets the generic fix", "", "production dependencies only", nil},
		{"python 3.11 is told to declare 3.12 or 3.13", "", `declares Python "3.11"`, python311},
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
			source := writeNodeSource(t, tc.engines)
			if tc.files != nil {
				source = writeSource(t, tc.files)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source})
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
