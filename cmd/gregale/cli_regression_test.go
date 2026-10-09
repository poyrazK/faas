package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type forbiddenAutomationInput struct{ t *testing.T }

func (r forbiddenAutomationInput) Read([]byte) (int, error) {
	r.t.Fatal("non-interactive command read stdin")
	return 0, io.EOF
}

func setupCLIRegression(t *testing.T) {
	t.Helper()
	setupConnectionProfiles(t)
	oldMode, oldJSON, oldIn := nonInteractive, jsonOutput, osStdin
	nonInteractive, jsonOutput = false, false
	t.Cleanup(func() { nonInteractive, jsonOutput, osStdin = oldMode, oldJSON, oldIn })
}

func assertOneProblem(t *testing.T, output string) api.Problem {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var p api.Problem
	if err := decoder.Decode(&p); err != nil {
		t.Fatalf("invalid Problem: %v\n%s", err, output)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra stderr output: %v %s", err, output)
	}
	if p.Code == "" || p.Title == "" {
		t.Fatalf("incomplete Problem: %+v", p)
	}
	return p
}

func TestCLIRegressionAutomationPrefixAndForwarding(t *testing.T) {
	setupCLIRegression(t)
	for _, tc := range []struct {
		args, want []string
		enabled    bool
	}{
		{[]string{"--non-interactive", "--profile", "staging", "--json", "apps"}, []string{"--profile", "staging", "--json", "apps"}, true},
		{[]string{"--json", "--profile=staging", "--non-interactive", "apps"}, []string{"--json", "--profile=staging", "apps"}, true},
		{[]string{"--non-interactive", "--non-interactive=false", "apps"}, []string{"apps"}, false},
		{[]string{"app", "demo", "exec", "--", "--non-interactive", "--profile", "staging", "--json"}, []string{"app", "demo", "exec", "--", "--non-interactive", "--profile", "staging", "--json"}, false},
		{[]string{"login", "--token", "--non-interactive"}, []string{"login", "--token", "--non-interactive"}, false},
		{[]string{"--profile", "--non-interactive", "apps"}, []string{"--profile", "--non-interactive", "apps"}, false},
	} {
		nonInteractive = false
		original := append([]string(nil), tc.args...)
		got, err := extractAutomationFlag(tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) || nonInteractive != tc.enabled || !reflect.DeepEqual(tc.args, original) {
			t.Fatalf("args=%v got=%v mode=%t err=%v", tc.args, got, nonInteractive, err)
		}
	}
	nonInteractive = false
	if _, err := extractAutomationFlag([]string{"--non-interactive=maybe", "apps"}); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}

func TestCLIRegressionAutomationFailsBeforeInputOrRequests(t *testing.T) {
	setupCLIRegression(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	osStdin = forbiddenAutomationInput{t}
	for _, args := range [][]string{{"login"}, {"start"}, {"account", "delete"}, {"apps", "--yes=false", "demo"}, {"triggers", "delete", "trigger-1"}, {"orgs", "rm", "demo"}, {"billing", "cancel"}, {"signup"}, {"mfa", "disable"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			stderr, restore := captureStderr(t)
			var out bytes.Buffer
			code := captureStdoutSwap(t, &out, func() int { return run(append([]string{"--non-interactive", "--json"}, args...)) })
			restore()
			if code != 1 || out.Len() != 0 || requests.Load() != 0 {
				t.Fatalf("code=%d stdout=%s requests=%d stderr=%s", code, out.String(), requests.Load(), stderr.String())
			}
			assertOneProblem(t, stderr.String())
			if nonInteractive || jsonOutput || profileOverride != "" {
				t.Fatal("run leaked global state")
			}
		})
	}
	nonInteractive = true
	if stdoutIsTTY() || stdinIsTTY() {
		t.Fatal("automation reported a terminal")
	}
	if err := openBrowser("https://example.com"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatal("browser launch was not blocked")
	}
	if _, err := readInteractivePassword(bufio.NewReader(forbiddenAutomationInput{t}), "secret:"); err == nil {
		t.Fatal("secret prompt allowed")
	}
}

func TestCLIRegressionAutomationExplicitInputAndConfirmation(t *testing.T) {
	setupCLIRegression(t)
	token := testAPIKey('b')
	var logins, deletions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong credential")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			logins.Add(1)
			_, _ = io.WriteString(w, `{"id":"acct","email":"me@example.com","plan":"free"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/demo":
			deletions.Add(1)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	osStdin = strings.NewReader(token + "\n")
	stderr, restore := captureStderr(t)
	defer restore()
	var output bytes.Buffer
	code := captureStdoutSwap(t, &output, func() int {
		return run([]string{"--non-interactive", "--profile", "staging", "--json", "login", "--token-stdin"})
	})
	if code != 0 || logins.Load() != 1 || !json.Valid(output.Bytes()) {
		t.Fatalf("login code=%d output=%s stderr=%s", code, output.String(), stderr.String())
	}
	profileOverride = "staging"
	if loadToken() != token {
		t.Fatal("credential not saved in selected profile")
	}
	profileOverride = ""
	output.Reset()
	osStdin = forbiddenAutomationInput{t}
	code = captureStdoutSwap(t, &output, func() int {
		return run([]string{"--profile", "staging", "--json", "--non-interactive", "apps", "--yes", "demo"})
	})
	if code != 0 || deletions.Load() != 1 || !json.Valid(output.Bytes()) || stderr.String() != "" {
		t.Fatalf("delete code=%d output=%s stderr=%s", code, output.String(), stderr.String())
	}
}

func TestCLIRegressionProblemsRedactNestedSecretsAndPreserveMetadata(t *testing.T) {
	setupCLIRegression(t)
	token := testAPIKey('a')
	t.Setenv("FAAS_TOKEN", token)
	limit := int64(9007199254740993)
	original := api.Problem{Status: 401, Code: api.CodeAPIKeyExpired, Title: "Expired", Detail: "reflected " + token, Hint: "Sign in again.", Limit: &limit, Errors: []api.FieldError{{Field: "token", Got: token}}}
	for _, jsonMode := range []bool{false, true} {
		jsonOutput = jsonMode
		stderr, restore := captureStderr(t)
		var output bytes.Buffer
		code := captureStdoutSwap(t, &output, func() int { return printErr("Request failed", &APIError{Problem: original}) })
		restore()
		if code != 2 || output.Len() != 0 || strings.Contains(stderr.String(), token) || !strings.Contains(stderr.String(), "FAAS_TOKEN") {
			t.Fatalf("mode=%t code=%d stderr=%s", jsonMode, code, stderr.String())
		}
		if jsonMode {
			p := assertOneProblem(t, stderr.String())
			if p.Code != original.Code || p.Limit == nil || *p.Limit != limit || len(p.Errors) != 1 || p.Errors[0].Got != "[REDACTED]" {
				t.Fatalf("metadata changed: %+v", p)
			}
		}
	}
	if original.Detail != "reflected "+token || original.Errors[0].Got != token {
		t.Fatal("renderer mutated original API error")
	}
	nonInteractive = true
	var out, errOut bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &out, &errOut
	PrintProgress(osStdout, "uploading")
	osStdout, osStderr = oldOut, oldErr
	if out.Len() != 0 || !strings.Contains(errOut.String(), "uploading") {
		t.Fatal("progress polluted stdout")
	}
}

func TestCLIRegressionDeployRecoveryAndCancellation(t *testing.T) {
	setupCLIRegression(t)
	jsonOutput = true
	profileOverride = "staging"
	key, _ := deployIdempotencyKey("", deployIdempotencyIntent{Slug: "demo", Image: "image@sha256:abc"})
	stderr, restore := captureStderr(t)
	code := printDeploySubmissionError("Deploy failed", context.DeadlineExceeded, "demo", key, "submission", "")
	if code != 3 {
		t.Fatalf("submission exit=%d", code)
	}
	assertOneProblem(t, stderr.String())
	var report struct {
		Recovery deployRecovery `json:"recovery"`
	}
	if err := json.Unmarshal([]byte(stderr.String()), &report); err != nil {
		t.Fatal(err)
	}
	if report.Recovery.IdempotencyKey != key || report.Recovery.InspectCommand != "gregale --profile staging deployments --app demo" || report.Recovery.RetryFlag != "--idempotency-key="+key {
		t.Fatalf("recovery=%+v", report.Recovery)
	}
	retryKey, _ := deployIdempotencyKey(key, deployIdempotencyIntent{Slug: "demo", Image: "image@sha256:abc"})
	if deployOperationIdempotencyKey(key, "json") != deployOperationIdempotencyKey(retryKey, "json") {
		t.Fatal("retry changed wire key")
	}
	restore()
	for _, rollout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		dep := api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"}
		if rollout {
			dep.Status = "live"
			dep.CanaryTotalSteps = 2
			dep.RolloutState = "running"
			dep.TrafficPercent = 50
		}
		stderr, restore := captureStderr(t)
		var out bytes.Buffer
		code = captureStdoutSwap(t, &out, func() int {
			return writeWaitedDeploymentReceiptUntilWithOptions(ctx, nil, dep, nil, "", "", "demo", time.Second, rollout, false)
		})
		restore()
		var receipt DeployReceipt
		if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		stage := "deployment"
		if rollout {
			stage = "rollout"
		}
		if code != 130 || !receipt.Interrupted || receipt.TimedOut || receipt.ID != "d1" || receipt.WaitStage != stage || receipt.Recovery == nil || !strings.HasPrefix(receipt.ResumeCommand, "gregale --profile staging deployment wait d1") {
			t.Fatalf("exit=%d receipt=%+v stderr=%s", code, receipt, stderr.String())
		}
		assertOneProblem(t, stderr.String())
	}
}

func TestCLIRegressionLoginRedactsUnsavedExplicitCredential(t *testing.T) {
	setupCLIRegression(t)
	token := "opaque-ci-credential-that-is-not-a-gregale-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: 401, Code: api.CodeUnauthorized, Title: "Rejected " + token, Detail: token})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	stderr, restore := captureStderr(t)
	defer restore()
	var out bytes.Buffer
	code := captureStdoutSwap(t, &out, func() int { return run([]string{"--non-interactive", "--json", "login", "--token", token}) })
	p := assertOneProblem(t, stderr.String())
	if code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), token) || !strings.Contains(p.Hint, "--token-stdin") || loginErrorCredential != "" {
		t.Fatalf("code=%d Problem=%+v stdout=%s", code, p, out.String())
	}
}

func TestCLIRegressionDeployRetryReusesActualWireKey(t *testing.T) {
	setupCLIRegression(t)
	t.Chdir(t.TempDir())
	var attempts atomic.Int32
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps" || r.URL.Path == "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/deployments":
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			if attempts.Add(1) == 1 {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "temporarily_unavailable", Title: "Response unavailable"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "accepted-deployment", AppID: "a1", Status: "pending"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	args := []string{"--non-interactive", "--profile", "staging", "--json", "deploy", "--image", "registry.example/demo@sha256:" + strings.Repeat("a", 64), "--name", "demo", "--no-wait"}
	invoke := func(arguments []string) (int, string, string) {
		stderr, restore := captureStderr(t)
		var out bytes.Buffer
		code := captureStdoutSwap(t, &out, func() int { return run(arguments) })
		restore()
		return code, out.String(), stderr.String()
	}
	code, out, errOut := invoke(args)
	if code != 3 || out != "" {
		t.Fatalf("first code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	assertOneProblem(t, errOut)
	var problem struct {
		Recovery deployRecovery `json:"recovery"`
	}
	if err := json.Unmarshal([]byte(errOut), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Recovery.IdempotencyKey == "" || problem.Recovery.Stage != "submission" {
		t.Fatalf("recovery=%+v", problem.Recovery)
	}
	code, out, errOut = invoke(append(append([]string(nil), args...), "--idempotency-key", problem.Recovery.IdempotencyKey))
	var receipt DeployReceipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("retry receipt: %v stdout=%s stderr=%s", err, out, errOut)
	}
	if code != 0 || receipt.ID != "accepted-deployment" || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("retry code=%d receipt=%+v wire keys=%v stderr=%s", code, receipt, keys, errOut)
	}
}

func TestCLIRegressionCommittedUploadKeepsKnownDeploymentID(t *testing.T) {
	setupCLIRegression(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/commit") {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: uploadSessionAlreadyCommittedCode, Title: "Already committed", Detail: "upload session already committed as deployment dep-123"})
		} else {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Read unavailable"})
		}
	}))
	defer server.Close()
	dep, err := commitUploadWithRetry(t.Context(), NewClient(server.URL, testAPIKey('a')), "upload-1")
	if err == nil || dep.ID != "dep-123" {
		t.Fatalf("lost committed deployment: dep=%+v err=%v", dep, err)
	}
}
