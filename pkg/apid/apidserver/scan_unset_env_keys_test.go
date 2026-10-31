package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The plan names, per workload, the Compose environment keys that will have
// no value, so a deploy does not start services without their configuration.
func TestScanProjectListsUnsetComposeEnvKeys(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	compose := `services:
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    environment:
      LOG_LEVEL: info
      STRIPE_KEY: sk_test_example
      PORT: "8080"
      GREGALE_TRACE: "1"
`
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, *e.Key(), composeTarGz(t, compose), map[string]string{"project_slug": "env-plan"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 1 || !slices.Equal(plan.Workloads[0].UnsetEnvKeys, []string{"LOG_LEVEL", "STRIPE_KEY"}) {
		t.Fatalf("workloads = %+v, want LOG_LEVEL and STRIPE_KEY unset and platform keys omitted", plan.Workloads)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sk_test_example")) {
		t.Fatal("the plan carried an environment value")
	}
	// Without x-gregale-env-import the plain value is reported, not imported.
	if want := map[string]string{"LOG_LEVEL": "not_opted_in", "STRIPE_KEY": "secret"}; !maps.Equal(plan.Workloads[0].UnsetEnvKeyReasons, want) || len(plan.Workloads[0].ImportedEnvKeys) != 0 {
		t.Fatalf("reasons = %v, imported = %v; want %v and nothing imported", plan.Workloads[0].UnsetEnvKeyReasons, plan.Workloads[0].ImportedEnvKeys, want)
	}
}

// composeTarGz is a source archive holding only compose.yaml.
func composeTarGz(t *testing.T, compose string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "compose.yaml", Mode: 0o644, Size: int64(len(compose)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(compose)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
