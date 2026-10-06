package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLogsDeploymentShowsBuildLogForFailedBuild — a failed build tells the
// customer to run `gregale logs <slug> --deployment <id>`, and production-us
// then answered "No running instance is available". A deployment that never
// served now streams its build/deploy log and ends with the failure reason;
// the runtime log endpoint is never called.
func TestLogsDeploymentShowsBuildLogForFailedBuild(t *testing.T) {
	resetJSONOut(t)
	const id = "7971fae0-6fd8-4425-b65e-e0c231ad1155"
	runtimeCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + id:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + id + `","app_id":"a1","status":"failed","error":"build exited 1"}`))
		case "/v1/deployments/" + id + "/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"npm ERR! missing script: start\"}\n\nevent: end\ndata: {}\n\n")
		case "/v1/apps/h3-slowstart/logs":
			runtimeCalled = true
			http.Error(w, "no", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	if code := cmdLogs([]string{"h3-slowstart", "--deployment", id}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if runtimeCalled {
		t.Fatal("runtime log endpoint was called for a deployment that never served")
	}
	if !strings.Contains(stdout.String(), "npm ERR! missing script: start") ||
		!strings.Contains(stderr.String(), "never served") || !strings.Contains(stderr.String(), "build exited 1") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// TestBuildStatusShowsFailureReason — `build status` showed only
// failure_class for a failed build; the reason is on the deployment row.
func TestBuildStatusShowsFailureReason(t *testing.T) {
	resetJSONOut(t)
	const buildID, depID = "11111111-1111-4111-8111-111111111111", "7971fae0-6fd8-4425-b65e-e0c231ad1155"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/builds/" + buildID:
			_, _ = w.Write([]byte(`{"id":"` + buildID + `","deployment_id":"` + depID + `","status":"failed","failure_class":"build_error"}`))
		case "/v1/deployments/" + depID:
			_, _ = w.Write([]byte(`{"id":"` + depID + `","status":"failed","error":"build exited 1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdBuildStatus([]string{buildID}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "failure_reason:") || !strings.Contains(out.String(), "build exited 1") ||
		!strings.Contains(out.String(), "gregale logs <slug> --deployment "+depID) {
		t.Fatalf("build status:\n%s", out.String())
	}
}
