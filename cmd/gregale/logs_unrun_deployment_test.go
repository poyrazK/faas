package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
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
	main.ResetJSONOut(t)
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
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()

	if code := main.CmdLogs([]string{"h3-slowstart", "--deployment", id}); code != 0 {
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
	main.ResetJSONOut(t)
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
	out, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdBuildStatus([]string{buildID}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "failure_reason:") || !strings.Contains(out.String(), "build exited 1") ||
		!strings.Contains(out.String(), "gregale logs <slug> --deployment "+depID) {
		t.Fatalf("build status:\n%s", out.String())
	}
}

// unrunDeploymentServer serves a failed deployment whose build log entry holds
// two lines, as the 2026-11-05 Node preflight failure did (#6633, #6635).
func unrunDeploymentServer(t *testing.T, id string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + id:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + id + `","app_id":"a1","status":"failed","error":"node toolchain HTTPS preflight failed","error_code":"build_infrastructure_failed"}`))
		case "/v1/deployments/" + id + "/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"[guest build output]\\nnode toolchain HTTPS preflight: timeout\\n\"}\n\nevent: end\ndata: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
}

func TestLogsDeploymentBuildLogKeepsLineBreaks(t *testing.T) {
	main.ResetJSONOut(t)
	const id = "641c7c00-b0bb-40c0-8950-4c6f5b613fb5"
	unrunDeploymentServer(t, id)
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()

	if code := main.CmdLogs([]string{"demo-app", "--deployment", id, "--explain"}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if want := "[guest build output]\nnode toolchain HTTPS preflight: timeout\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String()+stderr.String(), `\x0a`) {
		t.Fatalf("line breaks rendered as escapes: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// A failed deploy's recovery command is `logs <app> --deployment <id>`, and
// the starter AGENTS.md runs every command with --json. That command must
// answer in JSON: log records, then a summary with the failure (#6635).
func TestLogsDeploymentBuildLogJSONRecords(t *testing.T) {
	main.ResetJSONOut(t)
	const id = "641c7c00-b0bb-40c0-8950-4c6f5b613fb5"
	unrunDeploymentServer(t, id)
	recovery := main.FailedDeploymentRecovery("demo-app", api.DeploymentResponse{ID: id, Status: "failed"})
	args := strings.Fields(recovery.NextCommand)
	if len(args) < 2 || args[0] != "gregale" || args[1] != "logs" {
		t.Fatalf("recovery next_command = %q", recovery.NextCommand)
	}
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()
	*main.JsonOutput = true

	if code := main.CmdLogs(args[2:]); code != 0 {
		t.Fatalf("%v: exit = %d; stderr=%s", args, code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr is reserved for one JSON problem; got %q", stderr.String())
	}
	var records []map[string]string
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var record map[string]string
		if err := dec.Decode(&record); err != nil {
			t.Fatalf("stdout is not JSON records: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 2 || records[0]["type"] != "log" || records[0]["line"] != "[guest build output]\nnode toolchain HTTPS preflight: timeout\n" ||
		records[1]["type"] != "summary" || records[1]["status"] != "failed" || records[1]["error_code"] != "build_infrastructure_failed" || records[1]["deployment_id"] != id {
		t.Fatalf("records = %v", records)
	}
}
