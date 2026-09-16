package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func workspaceSelectorTarGz(t *testing.T, firstName, secondName string) []byte {
	t.Helper()
	files := map[string]string{
		"repo/pnpm-workspace.yaml":       "packages:\n  - frontend/api\n  - backend/api\n",
		"repo/frontend/api/package.json": `{"name":"` + firstName + `","scripts":{"start":"node server.js"}}`,
		"repo/backend/api/package.json":  `{"name":"` + secondName + `","scripts":{"start":"node server.js"}}`,
	}
	var raw bytes.Buffer
	zr := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zr)
	for _, name := range []string{"repo/pnpm-workspace.yaml", "repo/frontend/api/package.json", "repo/backend/api/package.json"} {
		body := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestScanProjectOnlyAcceptsRootQualifiedWorkspaceSelector(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, e.key, workspaceSelectorTarGz(t, "frontend-api", "backend-api"), map[string]string{
		"project_slug": "root-selector",
		"only":         "frontend/api",
	})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var response scanPlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Workloads) != 1 || response.Workloads[0].Name != "frontend-api" || response.Workloads[0].RootDir != "frontend/api" {
		t.Fatalf("root-qualified selection = %#v", response.Workloads)
	}
}

func TestScanProjectAmbiguousNameSelectorListsBothRoots(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, e.key, workspaceSelectorTarGz(t, "shared-api", "shared-api"), map[string]string{
		"project_slug": "ambiguous-selector",
		"only":         "shared-api",
	})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "frontend/api") || !strings.Contains(rec.Body.String(), "backend/api") {
		t.Fatalf("ambiguous selector response = %d %s", rec.Code, rec.Body.String())
	}
}
