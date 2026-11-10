package functionsrepo_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/functionsrepo"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeArchive writes files under a GitHub codeload-style wrapper directory.
func writeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "octo-shop-abc123/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "octo-shop-abc123/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
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
	p := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func member(name string) state.App {
	return state.App{ID: "app-" + name, Slug: "shop-" + name, Type: state.AppTypeFunction, ProjectID: "proj-1", WorkloadName: name}
}

func TestResolve(t *testing.T) {
	archive := writeArchive(t, map[string]string{
		"gregale.yaml": `functions:
  - name: api
    handler: src/api.mjs
    request_timeout_s: 45
    min_instances: 1
    queue_bindings:
      - name: orders
        queue_name: orders
  - name: digest
    root: jobs/digest
`,
		"src/api.mjs":              "export const config = { requestTimeoutS: 10, ramMb: 512 };\nexport default {};\n",
		"jobs/digest/handler.py":   "def handler(event, ctx): pass\n",
		"jobs/digest/data/big.bin": "not read",
		"README.md":                "shop",
	})

	api, ok, err := functionsrepo.Resolve(archive, member("api"))
	if err != nil || !ok {
		t.Fatalf("Resolve api = %v, %v", ok, err)
	}
	if api.Plan.Runtime != "node22" || api.Plan.Handler != "src/api.mjs" || api.Plan.Root != "." || api.Plan.Slug != "shop-api" {
		t.Errorf("api plan = %+v", api.Plan)
	}
	// gregale.yaml wins over code; code fills what yaml leaves unset.
	if api.Plan.Spec.RequestTimeoutS != 45 || api.Plan.Spec.RAMMB != 512 {
		t.Errorf("api spec = %+v, want yaml timeout 45 and RAM 512 from code", api.Plan.Spec)
	}
	m := api.Manifest
	if m.Function == nil || m.Function.Handler != "src/api.mjs" || m.Lifecycle == nil || *m.Lifecycle.RequestTimeoutS != 45 ||
		m.Scaling == nil || *m.Scaling.MinInstances != 1 || len(m.QueueBindings) != 1 || m.Triggers != nil {
		t.Errorf("api manifest = %+v", m)
	}

	digest, ok, err := functionsrepo.Resolve(archive, member("digest"))
	if err != nil || !ok || digest.Plan.Runtime != "python312" || digest.Plan.Handler != gregalemanifest.DefaultFunctionHandler || digest.Plan.Root != "jobs/digest" {
		t.Fatalf("Resolve digest = %+v, %v, %v", digest.Plan, ok, err)
	}

	if _, ok, err := functionsrepo.Resolve(archive, member("gone")); ok || err != nil {
		t.Fatalf("a function the project no longer declares: ok=%v err=%v, want not ok", ok, err)
	}
	notMember := member("api")
	notMember.ProjectID = ""
	if _, ok, _ := functionsrepo.Resolve(archive, notMember); ok {
		t.Fatal("a function app outside a project is not resolved")
	}
}

func TestResolveDiscoveredFunction(t *testing.T) {
	archive := writeArchive(t, map[string]string{
		"functions/hello/handler.js": "export const config = { cron: \"*/5 * * * *\", requestTimeoutS: 20 };\nexport default {};\n",
	})
	got, ok, err := functionsrepo.Resolve(archive, member("hello"))
	if err != nil || !ok {
		t.Fatalf("Resolve = %v, %v", ok, err)
	}
	if got.Plan.Root != "functions/hello" || got.Manifest.Lifecycle == nil || *got.Manifest.Lifecycle.RequestTimeoutS != 20 || got.Manifest.Triggers != nil {
		t.Fatalf("plan = %+v manifest = %+v; crons stay with the project reconcile", got.Plan, got.Manifest)
	}
}

func TestResolveRefusesMissingHandler(t *testing.T) {
	archive := writeArchive(t, map[string]string{
		"gregale.yaml": "functions:\n  - name: api\n    handler: src/missing.mjs\n",
	})
	if _, _, err := functionsrepo.Resolve(archive, member("api")); err == nil {
		t.Fatal("a missing handler file must fail the push")
	}
}
