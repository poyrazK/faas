// adr: 521
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationTestClient struct {
	customerOperationsClient
	get      func(context.Context) (api.OperationResponse, error)
	download func(io.Writer) (int64, error)
}

func (c customerOperationTestClient) GetOperation(ctx context.Context, _, _ string) (api.OperationResponse, error) {
	return c.get(ctx)
}
func (c customerOperationTestClient) DownloadOperationArtifact(_ context.Context, _, _, _ string, w io.Writer) (int64, error) {
	return c.download(w)
}

func TestCustomerOperationWatchWorkAndDeliveryAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		state    api.OperationState
		delivery string
		exit     int
	}{{api.OperationSucceeded, "dead", 0}, {api.OperationSucceeded, "pending", 0}, {api.OperationFailed, "succeeded", 1}, {api.OperationCancelled, "not_requested", 1}, {api.OperationRequiresReconciliation, "awaiting_outcome", 4}} {
		t.Run(string(tc.state)+tc.delivery, func(t *testing.T) {
			calls := 0
			client := customerOperationTestClient{get: func(context.Context) (api.OperationResponse, error) {
				calls++
				state := api.OperationRunning
				if calls > 2 {
					state = tc.state
				}
				return api.OperationResponse{ID: "op", State: state, Generation: 1, LatestSequence: 2, CompletionDelivery: api.OperationDeliveryResponse{State: tc.delivery}}, nil
			}}
			var out bytes.Buffer
			code, err := watchCustomerOperation(t.Context(), client, customerOperationCommand{app: "exports", id: "op", interval: time.Millisecond}, &out, true)
			if err != nil || code != tc.exit || calls != 3 {
				t.Fatalf("watch %d %v calls=%d", code, err, calls)
			}
			if lines := strings.Count(out.String(), "\n"); lines != 2 {
				t.Fatalf("duplicate snapshots: %s", out.String())
			}
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				var op api.OperationResponse
				if err := json.Unmarshal([]byte(line), &op); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCustomerOperationWatchCancellationAndDeadline(t *testing.T) {
	client := customerOperationTestClient{get: func(context.Context) (api.OperationResponse, error) {
		return api.OperationResponse{ID: "op", State: api.OperationRunning}, nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := watchCustomerOperation(ctx, client, customerOperationCommand{interval: time.Hour}, io.Discard, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	_, err = watchCustomerOperation(ctx, client, customerOperationCommand{interval: time.Hour}, io.Discard, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCustomerOperationDownloadPublishesOnlyVerifiedNewFiles(t *testing.T) {
	data := []byte("customer export\n")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	for _, kind := range []string{"success", "digest-mismatch", "partial", "existing", "symlink", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "export.csv")
			artifact := api.OperationResultArtifact{ID: "artifact", SizeBytes: int64(len(data)), SHA256: digest}
			client := customerOperationTestClient{get: func(context.Context) (api.OperationResponse, error) {
				a := []api.OperationResultArtifact{artifact}
				if kind == "ambiguous" {
					a = append(a, api.OperationResultArtifact{ID: "second"})
				}
				return api.OperationResponse{ID: "op", Artifacts: a}, nil
			}, download: func(w io.Writer) (int64, error) {
				if kind == "partial" {
					n, _ := w.Write(data[:3])
					return int64(n), io.ErrUnexpectedEOF
				}
				bytes := data
				if kind == "digest-mismatch" {
					bytes = []byte("different bytes")
				}
				n, err := w.Write(bytes)
				return int64(n), err
			}}
			if kind == "existing" {
				if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := downloadCustomerOperation(t.Context(), client, customerOperationCommand{app: "exports", id: "op", output: path})
			got, readErr := os.ReadFile(path)
			if kind == "success" {
				if err != nil || readErr != nil || !bytes.Equal(got, data) || receipt.SHA256 != digest {
					t.Fatalf("download %+v %v %v", receipt, err, readErr)
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0o600 {
					t.Fatal("download is not private")
				}
			} else {
				if err == nil {
					t.Fatal("published invalid output")
				}
				if kind == "existing" || kind == "symlink" {
					if string(got) != "keep me" {
						t.Fatal("clobbered existing output")
					}
				} else if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("left partial output")
				}
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".gregale-operation-") {
					t.Fatal("leaked temporary output")
				}
			}
		})
	}
}

func TestCustomerOperationCLIRecoveryAndRoutes(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(evidence, []byte("provider confirmed no effect"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests []api.OperationRecoveryRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fp_live_operator" {
			t.Error("credential missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/operations":
			q := r.URL.Query()
			if q.Get("scope") != "production" || q.Get("tenant_id") != "tenant" || q.Has("app_id") {
				t.Errorf("selectors %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(api.OperationListResponse{Operations: []api.OperationSummary{}, NextCursor: "cursor"})
		case "GET /v1/apps/exports/operations/op/events":
			_ = json.NewEncoder(w).Encode(api.OperationEventsResponse{Events: []api.OperationEvent{}, ResyncRequired: true})
		case "GET /v1/apps/exports/operations/op/executions":
			_ = json.NewEncoder(w).Encode(api.OperationExecutionsResponse{Executions: []api.OperationExecution{{Generation: 2, Attempts: 1}}})
		case "POST /v1/apps/exports/operations/op/recover":
			var req api.OperationRecoveryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			requests = append(requests, req)
			_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: "op", State: api.OperationAccepted, Generation: 2})
		case "POST /v1/apps/exports/operations/op/retry-delivery":
			_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: "op", State: api.OperationSucceeded, Generation: 1, CompletionDelivery: api.OperationDeliveryResponse{State: "pending"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_operator")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	recover := []string{"recover", "op", "--app", "exports", "--expected-generation", "1", "--recovery-id", "decision-1", "--resolution", "safe_to_retry", "--evidence-file", evidence}
	for _, args := range [][]string{{"list", "--app", "exports", "--scope", "production", "--tenant", "tenant"}, {"events", "op", "--app", "exports"}, {"executions", "op", "--app", "exports"}, recover, recover} {
		if code := cmdCustomerOperations(args); code != 0 {
			t.Fatalf("%v exit=%d", args, code)
		}
	}
	// The legacy API remains compatible; CLI retries now require a durable receipt.
	legacy, err := api.NewClient(srv.URL, "fp_live_operator").RetryOperationDelivery(t.Context(), "exports", "op")
	if err != nil || legacy.CompletionDelivery.State != "pending" {
		t.Fatalf("legacy notification API: %+v %v", legacy, err)
	}
	if len(requests) != 2 {
		t.Fatal("recovery requests missing")
	}
	a, _ := json.Marshal(requests[0])
	b, _ := json.Marshal(requests[1])
	if !bytes.Equal(a, b) || requests[0].ExpectedGeneration != 1 || requests[0].Evidence != "provider confirmed no effect" {
		t.Fatal("recovery identity/evidence drift")
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("JSON output polluted")
		}
	}
}

func TestCustomerOperationCLIRejectsUnfencedOrAmbiguousCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"list", "--app", "exports"}, {"list", "--app", "exports", "--scope", "production", "--limit", "101"}, {"get", "op"}, {"get", "op", "extra", "--app", "exports"}, {"watch", "op", "--app", "exports", "--interval", "0s"}, {"download", "op", "--app", "exports"}, {"recover", "op", "--app", "exports"}, {"cancel", "op", "--app", "exports"}, {"retry-delivery", "op", "--app", "exports", "--resolution", "safe_to_retry"}} {
		if _, err := parseCustomerOperationCommand(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	command, ok := lookupCliCommand("customer-operations")
	if !ok || len(command.Subcommands) != 15 {
		t.Fatal("command help/completion manifest missing")
	}
	if _, ok := lookupCliCommand("operations"); !ok {
		t.Fatal("exclusive operations compatibility lost")
	}
}

func TestCustomerOperationMissingVerbPrintsRegisteredUsage(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = oldJSON })
	var out bytes.Buffer
	code := 0
	if err := captureStderrSwap(t, &out, func() int {
		code = cmdCustomerOperations(nil)
		return code
	}); err != nil {
		t.Fatal(err)
	}
	if code != 1 || !strings.HasPrefix(out.String(), "usage: gregale customer-operations ") || !strings.Contains(out.String(), "Docs: "+docsURLForTopic("customer-operations")) {
		t.Fatalf("missing command did not identify its usage and documentation: exit=%d %q", code, out.String())
	}
}

func TestCustomerOperationRecoveryFileValidation(t *testing.T) {
	dir := t.TempDir()
	evidence, result := filepath.Join(dir, "evidence"), filepath.Join(dir, "result")
	write := func(path string, body []byte) {
		t.Helper()
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(evidence, []byte("provider confirms export stored"))
	write(result, []byte(`{"file":"export.csv"}`))
	req := api.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 2, Resolution: "succeeded"}
	if err := loadCustomerOperationRecovery(&req, evidence, result); err != nil || !json.Valid(req.Result) {
		t.Fatal("valid recovery files rejected", err)
	}
	for _, body := range [][]byte{[]byte("   "), {0xff}, []byte("has\x00nul"), bytes.Repeat([]byte("a"), api.OperationRecoveryEvidenceMaxBytes+1)} {
		write(evidence, body)
		if err := loadCustomerOperationRecovery(&req, evidence, result); err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
	write(evidence, []byte("confirmed"))
	write(result, []byte(`{"bad":`))
	if err := loadCustomerOperationRecovery(&req, evidence, result); err == nil {
		t.Fatal("malformed result accepted")
	}
	req.Resolution = "safe_to_retry"
	if err := loadCustomerOperationRecovery(&req, evidence, result); err == nil {
		t.Fatal("retry accepted an amended result")
	}
}

func TestCustomerOperationRecoveryRejectsSymlinkInputs(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "evidence"), filepath.Join(dir, "alias")
	if err := os.WriteFile(target, []byte("provider confirmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCustomerOperationFile(link, api.OperationRecoveryEvidenceMaxBytes); err == nil {
		t.Fatal("customer file guard bypassed")
	}
}
