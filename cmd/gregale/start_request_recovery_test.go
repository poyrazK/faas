package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func startRecoveryRunner(t *testing.T, input, publicURL string, logs http.HandlerFunc) (*startRunner, *atomic.Int32) {
	t.Helper()
	var mutations atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: publicURL})
		case "/v1/apps/first-app/logs":
			if logs != nil {
				logs(w, r)
				return
			}
			fallthrough
		default:
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, startTestSource(t), input)
	runner.session.AppID, runner.session.DeploymentID, runner.session.Status = "app-1", "d1", statusLive
	if err := saveStartSession(runner.statePath, runner.session); err != nil {
		t.Fatal(err)
	}
	runner.deploy = func(context.Context, []string, bool, ...deployExecution) int {
		t.Fatal("request recovery submitted a deployment")
		return 1
	}
	return runner, &mutations
}

func TestStartFirstResponseRetryReusesDeployment(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var requests, logs atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.Path != "/" {
			t.Errorf("unexpected public check: %s", r.URL)
		}
		w.Header().Set(api.DeploymentIDHeader, "d1")
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "Still starting")
			return
		}
		_, _ = fmt.Fprint(w, "Hello!")
	}))
	defer public.Close()
	runner, mutations := startRecoveryRunner(t, "\n", public.URL, func(w http.ResponseWriter, r *http.Request) {
		logs.Add(1)
		if r.URL.Query().Get("follow") != "0" || r.URL.Query().Get("deployment") != "" {
			t.Errorf("free-plan log request was not bounded and allowed: %s", r.URL)
		}
		_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"Waiting for the database\",\"deployment_id\":\"d1\"}\n\n")
	})
	before, _ := os.ReadFile(runner.statePath)
	if code := runner.checkFirstResponse("/", ""); code != 0 || requests.Load() != 2 || logs.Load() != 1 || mutations.Load() != 0 || runner.lastRequest == nil || runner.lastRequest.servedDeployment != "d1" {
		t.Fatalf("code=%d requests=%d logs=%d mutations=%d stderr=%s stdout=%s", code, requests.Load(), logs.Load(), mutations.Load(), stderr(), stdout)
	}
	after, _ := os.ReadFile(runner.statePath)
	if !bytes.Equal(before, after) || runner.session.DeploymentID != "d1" || strings.Count(stdout.String(), "Your app answered.") != 1 || !strings.Contains(stdout.String(), "Waiting for the database") {
		t.Fatalf("retry changed recovery metadata or reported false success: %s", stdout)
	}
}

func TestStartMissingRouteCanCheckAnotherPathWithoutPersistingQuery(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var requests atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("alternate path escaped the app") }))
	defer other.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/healthz" || r.URL.Query().Get("token") != "ephemeral-query-value" || r.Header.Get("Authorization") != "" {
			t.Errorf("wrong alternate request: %s", r.URL)
		}
		w.Header().Set(api.DeploymentIDHeader, "d1")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer public.Close()
	input := "2\n" + other.URL + "/escape\n//invalid-host/escape\n/healthz?token=ephemeral-query-value\n"
	runner, mutations := startRecoveryRunner(t, input, public.URL, nil)
	before, _ := os.ReadFile(runner.statePath)
	if code := runner.checkFirstResponse("/", ""); code != 0 || requests.Load() != 2 || mutations.Load() != 0 || runner.lastRequest == nil || runner.lastRequest.path != "/healthz?token=ephemeral-query-value" {
		t.Fatalf("code=%d requests=%d mutations=%d stderr=%s stdout=%s", code, requests.Load(), mutations.Load(), stderr(), stdout)
	}
	after, _ := os.ReadFile(runner.statePath)
	if !bytes.Equal(before, after) || bytes.Contains(after, []byte("ephemeral-query-value")) {
		t.Fatal("alternate path/query became durable session state")
	}
}

func TestStartFirstResponseRecoveryCannotBypassGreetingOrRevision(t *testing.T) {
	for _, mismatch := range []string{"greeting", "revision"} {
		t.Run(mismatch, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			var requests atomic.Int32
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					t.Error("greeting verification changed its path")
				}
				deployment, greeting := "d1", "new greeting"
				if requests.Add(1) == 1 {
					if mismatch == "greeting" {
						greeting = "old greeting"
					} else {
						deployment = "old-deploy"
					}
				}
				w.Header().Set(api.DeploymentIDHeader, deployment)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": greeting})
			}))
			defer public.Close()
			runner, mutations := startRecoveryRunner(t, "\n", public.URL, nil)
			if code := runner.checkFirstResponse("/", "new greeting"); code != 0 || requests.Load() != 2 || mutations.Load() != 0 || strings.Count(stdout.String(), "Your app answered.") != 1 || strings.Contains(stdout.String(), "Check another GET path") {
				t.Fatalf("code=%d requests=%d stderr=%s stdout=%s", code, requests.Load(), stderr(), stdout)
			}
		})
	}
}

func TestStartUnresolvedRequestFinishEOFAndCancelKeepFailure(t *testing.T) {
	for _, outcome := range []string{"finish", "EOF", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, _ := startTestEnvironment(t)
			var requests atomic.Int32
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer public.Close()
			input := ""
			if outcome == "finish" {
				input = "3\n"
			}
			runner, mutations := startRecoveryRunner(t, input, public.URL, nil)
			want := 1
			if outcome == "cancel" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				runner.ctx = ctx
				runner.prompt.reader = bufio.NewReader(&startCancelRequestInput{cancel: cancel})
				want = 130
			}
			if code := runner.checkFirstResponse("/", ""); code != want || requests.Load() != 1 || mutations.Load() != 0 || runner.lastRequest != nil || runner.session.DeploymentID != "d1" || strings.Contains(stdout.String(), "Your app answered.") {
				t.Fatalf("code=%d want=%d requests=%d mutations=%d stdout=%s", code, want, requests.Load(), mutations.Load(), stdout)
			}
		})
	}
}

type startCancelRequestInput struct{ cancel context.CancelFunc }

func (r *startCancelRequestInput) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}

func TestStartAppIdentityFailureCannotFetchRuntimeLogs(t *testing.T) {
	startTestEnvironment(t)
	runner, mutations := startRecoveryRunner(t, "2\n", "https://must-not-contact.example.test", func(http.ResponseWriter, *http.Request) { t.Error("identity mismatch fetched logs") })
	runner.session.AppID = "different-app"
	if code := runner.checkFirstResponse("/", ""); code == 0 || mutations.Load() != 0 || runner.lastRequest != nil {
		t.Fatalf("identity mismatch reported success: code=%d mutations=%d", code, mutations.Load())
	}
}

func TestStartRequestRecoveryCancellationWhileFetchingLogs(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer public.Close()
	runner, mutations := startRecoveryRunner(t, "", public.URL, func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	runner.ctx = ctx
	if code := runner.checkFirstResponse("/", ""); code != 130 || mutations.Load() != 0 || strings.Contains(stdout.String(), "Choose [") {
		t.Fatalf("cancellation did not stop the log fetch: code=%d stdout=%s", code, stdout)
	}
}

func TestStartPrivateRequestRecoveryUsesAuthenticatedInvocation(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var invocations, deployments atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+loadToken() {
			t.Error("account credentials did not stay on the control plane")
		}
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", RequireAuthn: true, CanonicalURL: "https://must-not-contact.example.test"})
		case "/v1/apps/first-app/invoke":
			var request api.InvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != http.MethodGet || request.Path != "/" {
				t.Errorf("retry changed the invocation: %+v, %v", request, err)
			}
			if invocations.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(api.InvokeResponse{ID: "i1", Status: "failed", Error: "Temporary error"})
			} else {
				_ = json.NewEncoder(w).Encode(api.InvokeResponse{ID: "i2", Status: "completed", Result: json.RawMessage(`{"message":"hello"}`)})
			}
		case "/v1/apps/first-app/deployments":
			deployments.Add(1)
		default:
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, startTestSource(t), "\n")
	runner.session.AppID, runner.session.DeploymentID = "app-1", "d1"
	if code := runner.checkFirstResponse("/", ""); code != 0 || invocations.Load() != 2 || deployments.Load() != 0 || runner.lastRequest == nil || runner.lastRequest.invocationID != "i2" {
		t.Fatalf("code=%d invocations=%d deployments=%d stderr=%s stdout=%s", code, invocations.Load(), deployments.Load(), stderr(), stdout)
	}
	if strings.Contains(stdout.String(), loadToken()) || strings.Contains(stdout.String(), "HTTP 200") || strings.Contains(stdout.String(), "Verified URL") {
		t.Fatalf("private invocation falsely proved public HTTP: %s", stdout)
	}
}

func TestStartRequestLogsAreBoundedScopedAndRedacted(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	var logs atomic.Int32
	runner, _ := startRecoveryRunner(t, "", "", func(w http.ResponseWriter, r *http.Request) {
		logs.Add(1)
		if r.URL.Query().Get("follow") != "0" || r.URL.Query().Get("deployment") != "d1" {
			t.Errorf("paid-plan log scope changed: %s", r.URL)
		}
		for i := 0; i < 12; i++ {
			line := fmt.Sprintf("error-%02d %s private-log-value-123\x1b\nsecond line", i, loadToken())
			encoded, _ := json.Marshal(map[string]string{"line": line, "deployment_id": "d1"})
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", encoded)
		}
		_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"wrong-deployment-log\",\"deployment_id\":\"other-deploy\"}\n\n")
		_, _ = fmt.Fprint(w, "event: stage\ndata: {\"line\":\"not-a-log-frame\"}\n\n")
	})
	runner.account.Plan = "pro"
	runner.secretsFile = filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(runner.secretsFile, []byte("PASSWORD=private-log-value-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.showRequestLogs(5)
	got := stdout.String()
	if logs.Load() != 1 || strings.Count(got, "error-") != 5 || !strings.Contains(got, "error-11") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("incorrect log preview: %s", got)
	}
	for _, forbidden := range []string{loadToken(), "private-log-value-123", "wrong-deployment-log", "not-a-log-frame", "\x1b"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("log preview leaked %q: %s", forbidden, got)
		}
	}
}

func TestStartStarterBlankGreetingFinishesWithoutChanges(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	runner := startTestRunner(t, source, "\n")
	runner.session.Template = "hello-node"
	before, _ := os.ReadFile(filepath.Join(source, "handler.js"))
	if code := runner.starterWalkthrough(); code != 0 {
		t.Fatalf("blank greeting code=%d", code)
	}
	after, _ := os.ReadFile(filepath.Join(source, "handler.js"))
	if !bytes.Equal(before, after) {
		t.Fatal("finishing the walkthrough edited the source")
	}
}

func TestStartStarterAppIdentityChangeRefusesEdit(t *testing.T) {
	startTestEnvironment(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "replacement-app", Slug: "first-app"})
	})
	source := startTestSource(t)
	runner := startTestRunner(t, source, "new greeting\ny\n")
	runner.session.Template, runner.session.AppID = "hello-node", "app-1"
	before, _ := os.ReadFile(filepath.Join(source, "handler.js"))
	if code := runner.starterWalkthrough(); code == 0 {
		t.Fatal("replacement app accepted a greeting deployment")
	}
	after, _ := os.ReadFile(filepath.Join(source, "handler.js"))
	if !bytes.Equal(before, after) {
		t.Fatal("app identity mismatch changed local source")
	}
}

func TestStartStarterChangedFileIsNotWrittenOrDeployed(t *testing.T) {
	startTestEnvironment(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
	})
	source := startTestSource(t)
	runner := startTestRunner(t, source, "")
	runner.session.Template = "hello-node"
	input := &startChangedGreetingInput{path: filepath.Join(source, "handler.js")}
	runner.prompt.reader = bufio.NewReader(input)
	runner.deploy = func(context.Context, []string, bool, ...deployExecution) int {
		t.Error("stale preview submitted a deployment")
		return 1
	}
	if code := runner.starterWalkthrough(); code == 0 {
		t.Fatal("changed source accepted the stale edit")
	}
	after, _ := os.ReadFile(input.path)
	if !bytes.Equal(after, input.externalEdit) {
		t.Fatal("combined confirmation overwrote the user's concurrent edit")
	}
}

type startChangedGreetingInput struct {
	path         string
	reads        int
	externalEdit []byte
}

func (r *startChangedGreetingInput) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(p, "new greeting\n"), nil
	}
	if r.reads > 2 {
		return 0, io.EOF
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return 0, err
	}
	r.externalEdit = append(data, []byte("\n// edited in my editor\n")...)
	if err := os.WriteFile(r.path, r.externalEdit, 0o640); err != nil {
		return 0, err
	}
	return copy(p, "y\n"), nil
}
