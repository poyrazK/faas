// adr: 600
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type recoveryReceiptTestClient struct {
	mu                    sync.Mutex
	origin, account, path string
	now                   time.Time
	posts, decisions      int
	lose                  bool
	request               api.OperationRecoveryRequest
	decision              api.OperationRecoveryDecision
}

func (f *recoveryReceiptTestClient) BaseURL() string { return f.origin }
func (f *recoveryReceiptTestClient) Whoami(context.Context) (api.AccountResponse, error) {
	return api.AccountResponse{ID: f.account}, nil
}
func (f *recoveryReceiptTestClient) GetOperation(context.Context, string, string) (api.OperationResponse, error) {
	return api.OperationResponse{ID: deliveryTestOperation, State: api.OperationRequiresReconciliation, Generation: 1, ExpiresAt: f.now.Add(time.Hour)}, nil
}
func (f *recoveryReceiptTestClient) RecoverOperationWithReceipt(_ context.Context, _, _ string, req api.OperationRecoveryRequest) (api.OperationRecoveryDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := os.Stat(f.path); err != nil {
		return api.OperationRecoveryDecision{}, errors.New("mutation preceded receipt publication")
	}
	f.posts++
	raw, _ := json.Marshal(req)
	fp, _ := operations.InputFingerprint(raw)
	if f.decisions == 0 {
		f.decisions++
		f.request = req
		generation, state := req.ExpectedGeneration, api.OperationState(req.Resolution)
		if req.Resolution == "safe_to_retry" {
			generation++
			state = api.OperationAccepted
		}
		f.decision = api.OperationRecoveryDecision{OperationID: deliveryTestOperation, RecoveryID: req.RecoveryID, RequestFingerprint: fp, ExpectedGeneration: req.ExpectedGeneration, Generation: generation, ExpectedInspectionRevision: req.ExpectedInspectionRevision, Resolution: req.Resolution, State: state, InvocationID: deliveryTestID, RecordedAt: f.now, ExpiresAt: f.now.Add(time.Hour)}
	} else if f.decision.RequestFingerprint != fp {
		return api.OperationRecoveryDecision{}, errors.New("changed decision")
	}
	if f.lose {
		f.lose = false
		return api.OperationRecoveryDecision{}, context.DeadlineExceeded
	}
	return f.decision, nil
}
func newRecoveryReceiptTest(t *testing.T, resolution string) (*recoveryReceiptTestClient, customerOperationCommand) {
	t.Helper()
	dir := t.TempDir()
	evidence := filepath.Join(dir, "evidence.txt")
	result := filepath.Join(dir, "result.json")
	if err := os.WriteFile(evidence, []byte("provider ledger checked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result, []byte(`{"file":"verified.csv","wide":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := &recoveryReceiptTestClient{origin: "https://api.example.com", account: deliveryTestAccount, path: filepath.Join(dir, "recovery.json"), now: time.Now().UTC()}
	args := []string{"recover", deliveryTestOperation, "--app", "exports", "--expected-generation", "1", "--recovery-id", "check-42", "--resolution", resolution, "--evidence-file", evidence, "--inspection-revision", "sha256:" + strings.Repeat("a", 64), "--receipt-file", f.path}
	if resolution == "succeeded" {
		args = append(args, "--result-file", result)
	}
	c, err := parseCustomerOperationCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}
func TestCustomerOperationRecoveryReceiptLostResponse(t *testing.T) {
	for _, resolution := range []string{"safe_to_retry", "succeeded", "failed", "cancelled"} {
		t.Run(resolution, func(t *testing.T) {
			f, c := newRecoveryReceiptTest(t, resolution)
			f.lose = true
			if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err == nil {
				t.Fatal("lost response not surfaced")
			}
			before, err := os.ReadFile(c.receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(filepath.Join(filepath.Dir(c.receipt), "evidence.txt")); err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(filepath.Join(filepath.Dir(c.receipt), "result.json")); err != nil {
				t.Fatal(err)
			}
			c, err = parseCustomerOperationCommand([]string{"recover", deliveryTestOperation, "--app", "exports", "--receipt-file", f.path})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			cached, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now.Add(2*time.Hour))
			if err != nil || cached != decision {
				t.Fatal("immutable acknowledgement", cached, err)
			}
			if f.posts != 2 || f.decisions != 1 {
				t.Fatal("decision repeated", f.posts, f.decisions)
			}
			after, _ := os.ReadFile(c.receipt)
			if !bytes.Equal(before, after) {
				t.Fatal("request changed")
			}
			for _, path := range []string{c.receipt, c.receipt + ".decided.json"} {
				info, e := os.Stat(path)
				if e != nil || info.Mode().Perm() != 0600 {
					t.Fatal("receipt not private", e)
				}
			}
			if f.request.Evidence != "provider ledger checked\n" {
				t.Fatal("evidence changed")
			}
			if resolution == "succeeded" && !bytes.Contains(f.request.Result, []byte("9007199254740993")) {
				t.Fatal("wide result corrupted")
			}
		})
	}
}

func TestCustomerOperationRecoveryReceiptAcceptsOnlyOneExecutionFamily(t *testing.T) {
	f, c := newRecoveryReceiptTest(t, "safe_to_retry")
	f.lose = true
	if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err == nil {
		t.Fatal("expected lost response")
	}
	raw, err := os.ReadFile(c.receipt)
	if err != nil {
		t.Fatal(err)
	}
	var receipt customerOperationRecoveryReceipt
	if err := decodeCustomerOperationReceipt(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"http", "workflow", "job", "mixed", "missing", "invalid-job"} {
		t.Run(family, func(t *testing.T) {
			decision := f.decision
			decision.InvocationID, decision.WorkflowRunID, decision.JobRunID = "", "", ""
			switch family {
			case "http":
				decision.InvocationID = deliveryTestID
			case "workflow":
				decision.WorkflowRunID = deliveryTestID
			case "job":
				decision.JobRunID = deliveryTestID
			case "mixed":
				decision.InvocationID, decision.JobRunID = deliveryTestID, deliveryTestID
			case "invalid-job":
				decision.JobRunID = "not-a-run-id"
			}
			err := validateCustomerOperationRecoveryDecision(decision, receipt)
			valid := family == "http" || family == "workflow" || family == "job"
			if (err == nil) != valid {
				t.Fatalf("valid=%v error=%v", valid, err)
			}
		})
	}
}
func TestCustomerOperationRecoveryReceiptConflicts(t *testing.T) {
	f, c := newRecoveryReceiptTest(t, "safe_to_retry")
	if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err != nil {
		t.Fatal(err)
	}
	resume := []string{"recover", deliveryTestOperation, "--app", "exports", "--receipt-file", f.path}
	for _, extra := range [][]string{{"--recovery-id", "other"}, {"--expected-generation", "2"}, {"--resolution", "failed"}, {"--inspection-revision", "sha256:" + strings.Repeat("b", 64)}} {
		changed, err := parseCustomerOperationCommand(append(append([]string{}, resume...), extra...))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = recoverCustomerOperationWithReceipt(t.Context(), f, changed, f.now); err == nil {
			t.Fatal("changed selector accepted", extra)
		}
	}
	f.account = "44444444-4444-4444-4444-444444444444"
	if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err == nil {
		t.Fatal("account crossed")
	}
	f.account = deliveryTestAccount
	f.origin = "https://other.example.com"
	if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err == nil {
		t.Fatal("origin crossed")
	}
	if f.posts != 1 {
		t.Fatal("conflict caused mutation")
	}
}
func TestCustomerOperationRecoveryReceiptRefusesUnsafeMutation(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "expired", "bad-ack", "oversized", "cancelled", "changed-evidence", "changed-result", "orphan-ack"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newRecoveryReceiptTest(t, "succeeded")
			ctx := t.Context()
			r := customerOperationRecoveryReceipt{Version: 1, API: f.origin, AccountID: f.account, App: c.app, OperationID: c.id, Request: c.recovery, CreatedAt: f.now.Add(-time.Minute), ReplayNotAfter: f.now.Add(time.Hour)}
			if kind == "expired" {
				r.ReplayNotAfter = f.now.Add(-time.Second)
			}
			if kind == "changed-evidence" {
				r.Request.Evidence = "edited"
			}
			if kind == "changed-result" {
				r.Request.Result = []byte(`{"file":"changed.csv"}`)
			}
			raw, _ := encodeCustomerOperationRecoveryReceipt(r)
			if kind == "oversized" {
				raw = bytes.Repeat([]byte("x"), api.OperationRecoveryReceiptMaxBytes+1)
			}
			if err := os.WriteFile(c.receipt, raw, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public":
				if err := os.Chmod(c.receipt, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(c.receipt, c.receipt+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(c.receipt+".target", c.receipt); err != nil {
					t.Fatal(err)
				}
			case "bad-ack", "orphan-ack":
				if err := os.WriteFile(c.receipt+".decided.json", []byte(`{"version":1,"request_sha256":"wrong"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "orphan-ack" {
					if err := os.Remove(c.receipt); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := recoverCustomerOperationWithReceipt(ctx, f, c, f.now); err == nil {
				t.Fatal("unsafe mutation allowed", kind)
			}
			if f.posts != 0 {
				t.Fatal("unsafe receipt caused mutation")
			}
		})
	}
}
func TestCustomerOperationRecoveryConcurrentReceipt(t *testing.T) {
	f, c := newRecoveryReceiptTest(t, "safe_to_retry")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.decisions != 1 {
		t.Fatal("multiple concurrent decisions")
	}
}
func TestCustomerOperationRecoveryReceiptPreviewAndSelectors(t *testing.T) {
	f, _ := newRecoveryReceiptTest(t, "safe_to_retry")
	for _, extra := range [][]string{{"--preview", "--expected-generation", "1", "--resolution", "failed"}, {"--self"}, {"--expected-generation", "0"}} {
		args := append([]string{"recover", deliveryTestOperation, "--app", "exports", "--receipt-file", f.path}, extra...)
		if _, err := parseCustomerOperationCommand(args); err == nil {
			t.Fatal("ambiguous apply accepted", args)
		}
	}
	c, err := parseCustomerOperationCommand([]string{"recover", deliveryTestOperation, "--app", "exports", "--receipt-file", f.path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err == nil {
		t.Fatal("new receipt inferred a decision")
	}
	if f.posts != 0 {
		t.Fatal("implicit recovery")
	}
}

func TestCustomerOperationRecoveryReceiptRetainsRequestDepth(t *testing.T) {
	f, c := newRecoveryReceiptTest(t, "succeeded")
	c.recovery.Result = []byte(strings.Repeat("[", api.OperationJSONMaxDepth-1) + "0" + strings.Repeat("]", api.OperationJSONMaxDepth-1))
	if _, err := recoverCustomerOperationWithReceipt(t.Context(), f, c, f.now); err != nil {
		t.Fatal("local receipt narrowed HTTP request depth", err)
	}
}
