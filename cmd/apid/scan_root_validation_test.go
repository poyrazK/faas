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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func composeRootTarGz(t *testing.T, contextPath string, extra map[string]string) []byte {
	t.Helper()
	entries := map[string]string{
		"repo/compose.yaml": "services:\n  ghost:\n    build:\n      context: " + contextPath + "\n    command: [\"sh\", \"-c\", \"echo ok\"]\n",
	}
	for name, body := range extra {
		entries["repo/"+name] = body
	}
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0), Size: int64(len(body))}); err != nil {
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
	return raw.Bytes()
}

func TestScanRejectsInvalidWorkloadRootsBeforeSigningPlan(t *testing.T) {
	cases := []struct {
		name        string
		contextPath string
		extra       map[string]string
		wantStatus  int
		wantRoot    string
	}{
		{name: "missing", contextPath: "./does-not-exist", wantStatus: http.StatusBadRequest, wantRoot: "does-not-exist"},
		{name: "regular_file", contextPath: "./not-a-directory", extra: map[string]string{"not-a-directory": "plain file\n"}, wantStatus: http.StatusBadRequest, wantRoot: "not-a-directory"},
		{name: "normalized_directory", contextPath: "./services/../services/api", extra: map[string]string{"services/api/Dockerfile": "FROM scratch\n"}, wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spool := t.TempDir()
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
			t.Setenv("FAAS_SPOOL_ROOT", spool)
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			req := scanAuditRegressionRequest(t, e.key, composeRootTarGz(t, tc.contextPath, tc.extra), map[string]string{
				"project_slug": "root-check-" + strings.ReplaceAll(tc.name, "_", "-"),
			})
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("scan status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var response scanPlanResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				rootFound := false
				for _, workload := range response.Workloads {
					rootFound = rootFound || workload.RootDir == "services/api"
				}
				if response.PlanToken == "" || !rootFound {
					t.Fatalf("valid normalized plan = %#v", response)
				}
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != api.CodeSourceInvalid || problem.Detail == "" || !bytes.Contains(rec.Body.Bytes(), []byte(tc.wantRoot)) {
				t.Fatalf("problem = %#v, body=%s", problem, rec.Body.String())
			}
		})
	}
}
