// adr: 20261031095954 — the CLI deploys the Data API runtime the control plane serves.
package main_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIDeployWithoutMigrationsUpgradesTheRuntime(t *testing.T) {
	embedded, err := templates.DataAPIRuntimeVersion()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		runtime     func(http.ResponseWriter)
		wantVersion string
		wantFail    bool
	}{
		{name: "served", runtime: func(w http.ResponseWriter) { main.WriteJSONTest(w, main.ServedDataAPIRuntime(t, "served-runtime")) }, wantVersion: main.ServedRuntimeVersion},
		{name: "older control plane", runtime: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Not found", "no such API route"))
		}, wantVersion: embedded},
		// Any other failure must not fall back to the CLI's own runtime.
		{name: "unavailable", runtime: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "data_api_runtime_unavailable", "Data API runtime unavailable", "retry"))
		}, wantFail: true},
		{name: "tampered", runtime: func(w http.ResponseWriter) {
			runtime := main.ServedDataAPIRuntime(t, "tampered")
			runtime.Archive[len(runtime.Archive)-1] ^= 1
			main.WriteJSONTest(w, runtime)
		}, wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Chdir(t.TempDir())
			var dockerfile, source int32
			base := main.ZeroConfigDeployServer(t, "notes-data", &dockerfile, &source, nil)
			defer base.Close()
			var uploaded map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data/data-api/runtime":
					tc.runtime(w)
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/notes-data":
					main.WriteJSONTest(w, map[string]any{})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data":
					main.WriteJSONTest(w, api.AppResponse{ID: "a1", Slug: "notes-data", Type: "app"})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/notes-data/deployments":
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					uploaded = main.UploadedSourceFiles(t, r.Header.Get("Content-Type"), body)
					r.Body = io.NopCloser(bytes.NewReader(body))
					base.Config.Handler.ServeHTTP(w, r)
				default:
					base.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			code := main.CmdDataAPI([]string{"deploy", "notes-data", "--timeout", "30s"})
			if tc.wantFail {
				if code == 0 || atomic.LoadInt32(&source) != 0 {
					t.Fatalf("deploy = %d, uploaded=%d; want a failure before any upload", code, source)
				}
				return
			}
			if code != 0 || atomic.LoadInt32(&dockerfile) != 1 || atomic.LoadInt32(&source) != 1 {
				t.Fatalf("deploy = %d dockerfile=%d source=%d", code, dockerfile, source)
			}
			if !strings.Contains(uploaded["data-api/runtime-version.json"], tc.wantVersion) {
				t.Fatalf("uploaded runtime-version.json = %q, want %s", uploaded["data-api/runtime-version.json"], tc.wantVersion)
			}
			if tc.wantVersion == main.ServedRuntimeVersion && !main.UploadedServerHas(uploaded, "served-runtime") {
				t.Fatal("deploy did not upload the served runtime")
			}
			if !strings.HasPrefix(uploaded["data-api/Procfile"], "web: node server.mjs") || strings.Contains(uploaded["data-api/Procfile"], "release:") {
				t.Fatalf("runtime-only deploy Procfile = %q", uploaded["data-api/Procfile"])
			}
		})
	}
}

func TestDataAPIDeployRejectsAReleaseCommandWithoutMigrations(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdDataAPI([]string{"deploy", "notes-data", "--release-command", "node migrate.mjs"}); code == 0 {
		t.Fatal("a release command without migrations was accepted")
	}
}

func TestReadDataAPIRuntimeArchiveRejectsUnsafeEntries(t *testing.T) {
	valid := map[string]string{"data-api/Dockerfile": "FROM x", "data-api/Procfile": "web: node server.mjs", "data-api/runtime-version.json": `{"version":"0123456789abcdef"}`}
	files, err := main.ReadDataAPIRuntimeArchive(tarGz(t, valid, nil))
	if err != nil || string(files["Dockerfile"]) != "FROM x" {
		t.Fatalf("valid archive: %v %v", files, err)
	}
	for name, extra := range map[string]map[string]string{
		"traversal":      {"data-api/../server.mjs": "x"},
		"outside root":   {"other/server.mjs": "x"},
		"absolute":       {"/data-api/server.mjs": "x"},
		"hidden":         {"data-api/.env": "SECRET=x"},
		"hidden dir":     {"data-api/.git/config": "x"},
		"unclean":        {"data-api/./server.mjs": "x"},
		"no version":     nil,
		"backslash path": {"data-api/a\\b.mjs": "x"},
	} {
		entries := map[string]string{}
		for k, v := range valid {
			entries[k] = v
		}
		if extra == nil {
			delete(entries, "data-api/runtime-version.json")
		}
		for k, v := range extra {
			entries[k] = v
		}
		if _, err := main.ReadDataAPIRuntimeArchive(tarGz(t, entries, nil)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := main.ReadDataAPIRuntimeArchive(tarGz(t, valid, &tar.Header{Name: "data-api/link.mjs", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})); err == nil {
		t.Error("symlink: accepted")
	}
	big := map[string]string{}
	for k, v := range valid {
		big[k] = v
	}
	big["data-api/big.bin"] = strings.Repeat("x", api.DataAPIRuntimeFilesMaxBytes+1)
	if _, err := main.ReadDataAPIRuntimeArchive(tarGz(t, big, nil)); err == nil {
		t.Error("oversized runtime: accepted")
	}
}

func TestDataAPIHealthFlagsARuntimeThatIsNotCurrent(t *testing.T) {
	for _, tc := range []struct {
		name, running, current, readiness, wantState string
		wantFinding                                  bool
	}{
		{name: "current", running: "0123456789abcdef", current: "0123456789abcdef", readiness: "ok", wantState: "current"},
		{name: "older", running: "fedcba9876543210", current: "0123456789abcdef", readiness: "ok", wantState: "not_current", wantFinding: true},
		{name: "predates reporting", running: "", current: "0123456789abcdef", readiness: "unready", wantState: "not_current", wantFinding: true},
		{name: "control plane cannot say", running: "fedcba9876543210", current: "", readiness: "ok", wantState: "unknown"},
		{name: "readiness unobserved", running: "", current: "0123456789abcdef", readiness: "unreachable", wantState: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := main.DataAPIHealthReport{Status: "healthy", CurrentFindings: []string{}, Readiness: main.DataAPIHealthProbe{State: tc.readiness, RuntimeVersion: tc.running}}
			main.AttachDataAPIRuntime(&report, tc.current)
			if report.Runtime == nil || report.Runtime.State != tc.wantState {
				t.Fatalf("runtime = %+v, want %s", report.Runtime, tc.wantState)
			}
			found := len(report.CurrentFindings) == 1 && report.CurrentFindings[0] == "runtime_not_current"
			if found != tc.wantFinding || report.Status != "healthy" {
				t.Fatalf("findings %v status %s", report.CurrentFindings, report.Status)
			}
			raw, _ := json.Marshal(report)
			if !strings.Contains(string(raw), `"runtime":{"running":`) {
				t.Fatalf("report JSON = %s", raw)
			}
		})
	}
}

func tarGz(t *testing.T, files map[string]string, extra *tar.Header) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		if err := tw.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
