//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These helper tests run in a subprocess so the lifecycle tests exercise actual
// foreground commands and process groups without requiring Node, curl, or Docker.
func TestManagedLocalAppHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_LOCAL_APP_HELPER")
	if mode == "" {
		return
	}
	fail := func(err error) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
	}
	_, _ = fmt.Fprintln(os.Stdout, "app-private-log")
	if mode == "exit" {
		os.Exit(7)
	}
	if mode == "exit-zero" {
		os.Exit(0)
	}
	if mode == "child" {
		signal.Ignore(syscall.SIGTERM)
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile("child-address", []byte(listener.Addr().String()), 0o600); err != nil {
			fail(err)
		}
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		os.Exit(0)
	}
	host, port := os.Getenv("HOST"), os.Getenv("PORT")
	baseURL := "http://" + net.JoinHostPort(host, port)
	if host != "127.0.0.1" || os.Getenv("GREGALE_TEST_HOST") != host || os.Getenv("GREGALE_TEST_PORT") != port || os.Getenv("GREGALE_TEST_URL") != baseURL || os.Getenv("FAAS_TOKEN") != "" {
		fail(fmt.Errorf("managed app received invalid environment"))
	}
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[len(os.Args)-1], "endpoint=") && os.Args[len(os.Args)-1] != "endpoint="+net.JoinHostPort(host, port) {
		fail(fmt.Errorf("managed app received invalid command placeholders"))
	}
	if mode == "descendant" {
		exe, err := os.Executable()
		if err != nil {
			fail(err)
		}
		child := exec.Command(exe, "-test.run=^TestManagedLocalAppHelper$")
		child.Env = append(os.Environ(), "GREGALE_LOCAL_APP_HELPER=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fail(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat("child-address"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				fail(fmt.Errorf("child did not start"))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		fail(err)
	}
	marker, _ := json.Marshal(map[string]string{"url": baseURL, "run": os.Getenv("GREGALE_TEST_RUN_ID"), "case": os.Getenv("GREGALE_TEST_CASE")})
	if err := appendLocalAppHelperFile("starts.jsonl", append(marker, '\n')); err != nil {
		fail(err)
	}
	started := time.Now()
	var mu sync.Mutex
	server := &http.Server{ReadHeaderTimeout: time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			if mode == "never-ready" || time.Since(started) < 120*time.Millisecond {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(204)
		case "/redirect":
			http.Redirect(w, request, "/health", http.StatusFound)
		case "/check":
			var data map[string]any
			_ = json.Unmarshal([]byte(os.Getenv("GREGALE_TEST_DATA_JSON")), &data)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"run": os.Getenv("GREGALE_TEST_RUN_ID"), "customer": data["customer"]})
		case "/crash":
			os.Exit(7)
		case "/slow":
			select {
			case <-request.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			mu.Lock()
			err := appendLocalAppHelperFile("order.log", []byte(strings.TrimPrefix(request.URL.Path, "/")+":"+os.Getenv("GREGALE_TEST_CASE")+"\n"))
			mu.Unlock()
			if err != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		}
	})
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		go func() {
			<-terminated
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
	}
	_ = server.Serve(listener)
	os.Exit(0)
}

func appendLocalAppHelperFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write(data)
	return err
}

func TestManagedLocalFixtureHelper(t *testing.T) {
	if os.Getenv("GREGALE_LOCAL_APP_HELPER") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", os.Getenv("GREGALE_TEST_URL")+"/"+os.Args[len(os.Args)-1], nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 204 {
		t.Fatalf("fixture status = %d", response.StatusCode)
	}
}

func managedLocalTestSpec(t *testing.T, mode string) *testLocalAppSpec {
	t.Helper()
	t.Setenv("GREGALE_LOCAL_APP_HELPER", mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &testLocalAppSpec{
		Command:         []string{exe, "-test.run=^TestManagedLocalAppHelper$", "--", "endpoint=${local.host}:${local.port}"},
		Readiness:       testLocalReadinessSpec{Path: "/health", Status: 204, Timeout: "3s"},
		ShutdownTimeout: "1s",
	}
}

func managedLocalFixtureCommand(t *testing.T, phase string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, "-test.run=^TestManagedLocalFixtureHelper$", "--", phase}
}

func captureManagedLocalOutput(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonOutput, osStdout, osStderr = true, stdout, stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	return stdout, stderr
}

func TestManagedLocalCLIStartsFreshAppsForRowsAndRepeats(t *testing.T) {
	dir := t.TempDir()
	spec := managedLocalTestSpec(t, "serve")
	t.Setenv("FAAS_TOKEN", "account-private-key")
	t.Setenv("PORT", "inherited-port")
	t.Setenv("HOST", "inherited-host")
	stdout, stderr := captureManagedLocalOutput(t)
	manifest := filepath.Join(dir, "gregale-test.yaml")
	// Marshal argv as YAML flow sequences using their JSON representation.
	argv, _ := json.Marshal(spec.Command)
	setup, _ := json.Marshal(managedLocalFixtureCommand(t, "setup"))
	trigger, _ := json.Marshal(managedLocalFixtureCommand(t, "trigger"))
	assertion, _ := json.Marshal(managedLocalFixtureCommand(t, "assert"))
	cleanup, _ := json.Marshal(managedLocalFixtureCommand(t, "cleanup"))
	contents := fmt.Sprintf(`version: 1
scenarios:
  api-smoke:
    project: api-smoke
    local:
      command: %s
      readiness: {path: /health, status: 204, timeout: 3s}
      shutdown_timeout: 1s
    setup: [%s]
    trigger: %s
    checks:
      - name: read
        method: GET
        path: /check
        expect:
          status: 200
          json: {'/run': '${run.id}', '/customer': '${data.customer}'}
    command: %s
    cleanup: [%s]
`, argv, setup, trigger, assertion, cleanup)
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(data, []byte(`[{"customer":"private-first"},{"customer":"private-second"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--engine", "local", "--validate", "--manifest", manifest, "--data", data}); code != 0 {
		t.Fatalf("validate exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "starts.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("validation started app: %v", err)
	}
	stdout.Reset()
	report, junit := filepath.Join(dir, "report.json"), filepath.Join(dir, "report.xml")
	if code := cmdTest([]string{"--engine", "local", "--scenario", "api-smoke", "--manifest", manifest, "--data", data, "--repeat", "2", "--report", report, "--junit", junit}); code != 0 {
		t.Fatalf("managed run exit %d: %s / %s", code, stdout, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 4 {
		t.Fatalf("receipts = %s (%v)", stdout, err)
	}
	for i, receipt := range receipts {
		if receipt.Status != "passed" || receipt.Case != fmt.Sprintf("row-%d", i%2+1) || receipt.Attempt != i/2+1 || receipt.Engine != "local" || receipt.Profile != "local" || receipt.Evidence != nil || receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.ReadinessStatus != 204 || receipt.LocalApp.ReadinessAttempts < 2 || receipt.LocalApp.Shutdown != "graceful" || receipt.LocalApp.ExitCode == nil || *receipt.LocalApp.ExitCode != 0 {
			t.Fatalf("receipt %d = %+v, app = %+v", i, receipt, receipt.LocalApp)
		}
		var names []string
		for _, phase := range receipt.Phases {
			names = append(names, phase.Name)
		}
		if strings.Join(names, ",") != "startup,setup,requests,checks,cleanup,shutdown" {
			t.Fatalf("phases = %v", names)
		}
	}
	starts, err := os.ReadFile(filepath.Join(dir, "starts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(starts), []byte("\n")) {
		var start map[string]string
		if err := json.Unmarshal(line, &start); err != nil {
			t.Fatal(err)
		}
		if runs[start["run"]] || start["run"] == "" {
			t.Fatalf("run identity reused: %v", start)
		}
		runs[start["run"]] = true
		assertManagedLocalPortReleased(t, strings.TrimPrefix(start["url"], "http://"))
	}
	if len(runs) != 4 {
		t.Fatalf("apps started = %d", len(runs))
	}
	order, err := os.ReadFile(filepath.Join(dir, "order.log"))
	if err != nil {
		t.Fatal(err)
	}
	var expected strings.Builder
	for i := 0; i < 4; i++ {
		for _, phase := range []string{"setup", "trigger", "assert", "cleanup"} {
			_, _ = fmt.Fprintf(&expected, "%s:row-%d\n", phase, i%2+1)
		}
	}
	if string(order) != expected.String() {
		t.Fatalf("fixture order = %q", order)
	}
	for _, path := range []string{report, junit} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"app-private-log", "account-private-key", "private-first", "private-second", "endpoint=", "TestManagedLocalAppHelper"} {
			if bytes.Contains(body, []byte(private)) {
				t.Errorf("%s leaked %q", path, private)
			}
		}
		if !bytes.Contains(body, []byte("local_app")) {
			t.Errorf("%s omitted managed app evidence", path)
		}
		if path == junit {
			var suite testJUnitSuite
			if err := xml.Unmarshal(body, &suite); err != nil || suite.Tests != 4 || suite.Failures != 0 {
				t.Fatalf("JUnit = %+v (%v)", suite, err)
			}
		}
	}
	if bytes.Contains(stderr.Bytes(), []byte("app-private-log")) {
		t.Fatal("successful app logs were emitted")
	}
}

func assertManagedLocalPortReleased(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		listener, err := net.Listen("tcp4", address)
		if err == nil {
			_ = listener.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed app left port %s occupied: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagedLocalFailuresStillCleanUpAndStop(t *testing.T) {
	for _, failure := range []string{"early-exit", "zero-exit", "readiness", "redirect", "request", "crash", "canceled", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			mode := "serve"
			if failure == "early-exit" {
				mode = "exit"
			} else if failure == "zero-exit" {
				mode = "exit-zero"
			} else if failure == "readiness" {
				mode = "never-ready"
			}
			spec := managedLocalTestSpec(t, mode)
			_, stderr := captureManagedLocalOutput(t)
			scenario := testScenario{Local: spec, Checks: []testHTTPRequest{{Name: "check", Method: "GET", Path: "/check", Expect: testHTTPExpect{Status: 200}}}, Cleanup: [][]string{{"sh", "-c", "printf cleaned > cleanup.txt"}}}
			ctx := context.Background()
			switch failure {
			case "readiness":
				spec.Readiness.Timeout = "1s"
			case "redirect":
				spec.Readiness.Path, spec.Readiness.Timeout = "/redirect", "1s"
			case "request":
				scenario.Checks[0].Expect.Status = 403
				scenario.Cleanup = append(scenario.Cleanup, managedLocalFixtureCommand(t, "cleanup"))
			case "crash":
				scenario.Checks[0].Path = "/crash"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
				scenario.Checks[0].Path = "/slow"
				scenario.Cleanup = append(scenario.Cleanup, managedLocalFixtureCommand(t, "cleanup"))
			case "cleanup":
				scenario.Cleanup = append(scenario.Cleanup, []string{"sh", "-c", "exit 1"}, managedLocalFixtureCommand(t, "cleanup"))
			}
			started := time.Now()
			receipt := runLocalTest(ctx, "failure", scenario, dir, "", testDataCase{})
			if receipt.Status != "failed" || (receipt.Error == "" && receipt.CleanupError == "") || receipt.LocalApp == nil || receipt.LocalApp.Shutdown == "not_started" || receipt.LocalApp.Shutdown == "failed" {
				t.Fatalf("failed run = %+v, app = %+v", receipt, receipt.LocalApp)
			}
			if time.Since(started) > 5*time.Second {
				t.Fatalf("failure did not cancel promptly: %s", time.Since(started))
			}
			if failure == "early-exit" || failure == "zero-exit" || failure == "crash" {
				code := 7
				if failure == "zero-exit" {
					code = 0
				}
				if !strings.Contains(receipt.Error, fmt.Sprintf("local app exited before shutdown (exit code %d)", code)) || receipt.LocalApp.ExitCode == nil || *receipt.LocalApp.ExitCode != code {
					t.Fatalf("unexpected exit evidence = %+v / %+v", receipt, receipt.LocalApp)
				}
			}
			if failure == "readiness" && (receipt.LocalApp.Ready || receipt.LocalApp.ReadinessStatus != 503) {
				t.Fatalf("readiness evidence = %+v", receipt.LocalApp)
			}
			if failure == "redirect" && (receipt.LocalApp.Ready || receipt.LocalApp.ReadinessStatus != 302) {
				t.Fatalf("redirect readiness evidence = %+v", receipt.LocalApp)
			}
			if body, err := os.ReadFile(filepath.Join(dir, "cleanup.txt")); err != nil || string(body) != "cleaned" {
				t.Fatalf("cleanup = %q (%v)", body, err)
			}
			if failure == "request" || failure == "canceled" || failure == "cleanup" {
				if body, err := os.ReadFile(filepath.Join(dir, "order.log")); err != nil || !bytes.Contains(body, []byte("cleanup:")) {
					t.Fatalf("cleanup could not reach app = %q (%v)", body, err)
				}
			}
			if !bytes.Contains(stderr.Bytes(), []byte("app-private-log")) {
				t.Fatal("failed app log missing from stderr")
			}
			if starts, err := os.ReadFile(filepath.Join(dir, "starts.jsonl")); err == nil {
				var start map[string]string
				if err := json.Unmarshal(bytes.TrimSpace(starts), &start); err != nil {
					t.Fatal(err)
				}
				assertManagedLocalPortReleased(t, strings.TrimPrefix(start["url"], "http://"))
			}
		})
	}
}

func TestManagedLocalForcesShutdownOfAppAndDescendants(t *testing.T) {
	for _, mode := range []string{"ignore-term", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			spec := managedLocalTestSpec(t, mode)
			captureManagedLocalOutput(t)
			scenario := testScenario{Local: spec, Cleanup: [][]string{managedLocalFixtureCommand(t, "cleanup")}}
			receipt := runLocalTest(context.Background(), "forced", scenario, dir, "", testDataCase{})
			if receipt.Status != "passed" || receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.Shutdown != "forced" {
				t.Fatalf("forced run = %+v, app = %+v", receipt, receipt.LocalApp)
			}
			if address, err := os.ReadFile(filepath.Join(dir, "child-address")); err == nil {
				assertManagedLocalPortReleased(t, string(address))
			} else if mode == "descendant" {
				t.Fatal(err)
			}
			starts, err := os.ReadFile(filepath.Join(dir, "starts.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var start map[string]string
			if err := json.Unmarshal(bytes.TrimSpace(starts), &start); err != nil {
				t.Fatal(err)
			}
			assertManagedLocalPortReleased(t, strings.TrimPrefix(start["url"], "http://"))
		})
	}
}

func TestManagedLocalOccupiedPortNeverStartsAppOrStopsExistingServer(t *testing.T) {
	dir := t.TempDir()
	spec := managedLocalTestSpec(t, "serve")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	receipt := runLocalTest(context.Background(), "occupied", testScenario{Local: spec}, dir, server.URL, testDataCase{})
	if receipt.Status != "failed" || !strings.Contains(receipt.Error, "reserve local app port") || receipt.LocalApp.Shutdown != "not_started" {
		t.Fatalf("occupied receipt = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(dir, "starts.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("app started on occupied port: %v", err)
	}
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("existing server was stopped: %v", err)
	}
	_ = response.Body.Close()
}

func TestManagedLocalNativeLoadAndProgress(t *testing.T) {
	dir := t.TempDir()
	spec := managedLocalTestSpec(t, "serve")
	_, stderr := captureManagedLocalOutput(t)
	scenario := testScenario{Local: spec, Checks: []testHTTPRequest{{Name: "health", Method: "GET", Path: "/health", Expect: testHTTPExpect{Status: 204}}}, Cleanup: [][]string{managedLocalFixtureCommand(t, "cleanup")}}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{VUs: 2, VUsSet: true, Iterations: 4, IterationsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Progress = func(update testLoadProgress) { printTestLoadProgress(osStderr, update) }
	receipt := runLocalTestWithLoad(context.Background(), "load", scenario, dir, "", testDataCase{}, cfg)
	if receipt.Status != "passed" || receipt.Load == nil || receipt.Load.IterationsCompleted != 4 || receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.Shutdown != "graceful" {
		t.Fatalf("managed load = %+v, app = %+v, load = %+v; stderr %s", receipt, receipt.LocalApp, receipt.Load, stderr)
	}
}

func TestManagedLocalCLIInterruptHelper(t *testing.T) {
	manifest := os.Getenv("GREGALE_LOCAL_CLI_HELPER_MANIFEST")
	if manifest == "" {
		return
	}
	jsonOutput = true
	os.Exit(cmdTest([]string{"--engine", "local", "--scenario", "api-smoke", "--manifest", manifest, "--report", filepath.Join(filepath.Dir(manifest), "report.json")}))
}

func TestManagedLocalCLITerminationCleansUpBeforeShutdown(t *testing.T) {
	for _, terminated := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(terminated.String(), func(t *testing.T) {
			dir := t.TempDir()
			spec := managedLocalTestSpec(t, "serve")
			argv, _ := json.Marshal(spec.Command)
			setup, _ := json.Marshal(managedLocalFixtureCommand(t, "setup"))
			cleanup, _ := json.Marshal(managedLocalFixtureCommand(t, "cleanup"))
			manifest := filepath.Join(dir, "gregale-test.yaml")
			contents := fmt.Sprintf(`version: 1
scenarios:
  api-smoke:
    project: my-api
    local:
      command: %s
      readiness: {path: /health, status: 204, timeout: 3s}
      shutdown_timeout: 1s
    setup: [%s]
    checks:
      - {name: slow, method: GET, path: /slow, expect: {status: 200}}
    cleanup: [%s]
`, argv, setup, cleanup)
			if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, exe, "-test.run=^TestManagedLocalCLIInterruptHelper$")
			command.Env = append(os.Environ(), "GREGALE_LOCAL_CLI_HELPER_MANIFEST="+manifest)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill() }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if order, err := os.ReadFile(filepath.Join(dir, "order.log")); err == nil && bytes.Contains(order, []byte("setup:")) {
					break
				}
				if time.Now().After(deadline) {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatalf("CLI did not start app and fixtures: %s", &output)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(terminated); err != nil {
				_ = command.Wait()
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil || ctx.Err() != nil {
				t.Fatalf("CLI did not report cancellation promptly: %v / %s", err, &output)
			}
			var receipts []testRunReceipt
			body, err := os.ReadFile(filepath.Join(dir, "report.json"))
			if err != nil {
				t.Fatalf("CLI omitted cancellation report: %v / %s", err, &output)
			}
			if err := json.Unmarshal(body, &receipts); err != nil || len(receipts) != 1 || receipts[0].Status != "failed" || receipts[0].LocalApp == nil || receipts[0].LocalApp.Shutdown != "graceful" {
				t.Fatalf("canceled CLI receipts = %s (%v)", body, err)
			}
			if order, err := os.ReadFile(filepath.Join(dir, "order.log")); err != nil || string(order) != "setup:\ncleanup:\n" {
				t.Fatalf("CLI cleanup did not reach app: %q (%v)", order, err)
			}
			starts, err := os.ReadFile(filepath.Join(dir, "starts.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var start map[string]string
			if err := json.Unmarshal(bytes.TrimSpace(starts), &start); err != nil {
				t.Fatal(err)
			}
			assertManagedLocalPortReleased(t, strings.TrimPrefix(start["url"], "http://"))
		})
	}
}

func TestManagedLocalStartFailureRunsCleanup(t *testing.T) {
	dir := t.TempDir()
	spec := &testLocalAppSpec{Command: []string{filepath.Join(dir, "missing-executable")}}
	receipt := runLocalTest(context.Background(), "missing", testScenario{Local: spec, Cleanup: [][]string{{"sh", "-c", "printf cleaned > cleanup.txt"}}}, dir, "", testDataCase{})
	if receipt.Status != "failed" || !strings.Contains(receipt.Error, "start local app") || receipt.LocalApp == nil || receipt.LocalApp.Shutdown != "not_started" {
		t.Fatalf("startup failure = %+v", receipt)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "cleanup.txt")); err != nil || string(body) != "cleaned" {
		t.Fatalf("startup cleanup = %q (%v)", body, err)
	}
}

func TestManagedLocalManifestRejectsInvalidSpec(t *testing.T) {
	for _, block := range []string{
		"command: []", "command: ['']", "command: [node, '${local.unknown}']", "command: [node, '${local.port']",
		"command: [node]\n      readiness: {path: '//example.test/health'}",
		"command: [node]\n      readiness: {path: '/health?token=secret'}",
		"command: [node]\n      readiness: {path: '/health#secret'}",
		"command: [node]\n      readiness: {path: '/${data.secret}'}",
		"command: [node]\n      readiness: {status: 199}",
		"command: [node]\n      readiness: {timeout: 0s}",
		"command: [node]\n      readiness: {timeout: 6m}",
		"command: [node]\n      readiness: {unknown: /health}",
		"command: [node]\n      shutdown_timeout: 31s",
		"command: [node]\n      unknown: true",
	} {
		t.Run(block, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gregale-test.yaml")
			contents := "version: 1\nscenarios:\n  api-smoke:\n    project: api-smoke\n    command: [true]\n    local:\n      " + block + "\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readTestManifest(path); err == nil {
				t.Fatal("invalid managed manifest accepted")
			}
		})
	}
}

func TestManagedLocalLogIsBoundedAndConcurrent(t *testing.T) {
	var log localAppLog
	var group sync.WaitGroup
	for i := 0; i < 3; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = io.WriteString(&log, strings.Repeat("x", localAppLogLimit*2))
			_ = log.String()
		}()
	}
	group.Wait()
	_, _ = io.WriteString(&log, "last-line")
	if output := log.String(); len(output) != localAppLogLimit || !strings.HasSuffix(output, "last-line") {
		t.Fatalf("bounded tail length = %s", strconv.Itoa(len(output)))
	}
}
