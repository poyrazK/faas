package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type commitInspectionClient struct {
	source       api.CommitSourceResponse
	receipt      api.CommitReceiptResponse
	operation    api.CommitOperationResponse
	blocked      api.CommitBlockedEventsResponse
	receiptErr   error
	operationErr error
	reads        int
}

func (c *commitInspectionClient) GetCommitSource(context.Context, string) (api.CommitSourceResponse, error) {
	return c.source, nil
}
func (c *commitInspectionClient) GetCommitReceipt(context.Context, string, string) (api.CommitReceiptResponse, error) {
	c.reads++
	return c.receipt, c.receiptErr
}
func (c *commitInspectionClient) GetCommitOperation(context.Context, string) (api.CommitOperationResponse, error) {
	return c.operation, c.operationErr
}
func (c *commitInspectionClient) ListCommitBlockedEvents(context.Context, string) (api.CommitBlockedEventsResponse, error) {
	return c.blocked, nil
}

func TestCommitInspectionPreservesUnknownAndRetainedFacts(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-10 * time.Minute)
	event := uuid.NewString()
	missing := &APIError{Problem: api.Problem{Status: http.StatusNotFound, Code: "commit_event_not_found"}}
	source := api.CommitSourceResponse{ID: uuid.NewString(), Enabled: true, OperationPolicy: "orders", RelayStatus: "healthy", LastCheckedAt: &now}
	for _, tc := range []struct {
		name                          string
		client                        commitInspectionClient
		acceptance, execution, commit string
		wantErr                       bool
	}{
		{"absent receipt is unknown", commitInspectionClient{source: source, receiptErr: missing}, "unknown", "unknown", "unknown", false},
		{"database failure is an error", commitInspectionClient{source: source, receiptErr: &APIError{Problem: api.Problem{Status: 503, Code: "capacity"}}}, "", "", "", true},
		{"unrelated 404 is an error", commitInspectionClient{source: source, receiptErr: &APIError{Problem: api.Problem{Status: 404, Code: "route_not_found"}}}, "", "", "", true},
		{"retained completion wins", commitInspectionClient{source: source, receipt: api.CommitReceiptResponse{OperationID: uuid.NewString()}, operation: api.CommitOperationResponse{State: "completed"}, blocked: api.CommitBlockedEventsResponse{Items: []api.CommitBlockedEventResponse{{EventID: event, ObservedAt: now}}}}, "accepted", "completed", "unknown", false},
		{"stale block is historical", commitInspectionClient{source: source, receiptErr: missing, blocked: api.CommitBlockedEventsResponse{Items: []api.CommitBlockedEventResponse{{EventID: event, ObservedAt: stale}}}}, "unknown", "unknown", "observed", false},
		{"accepted status read fails", commitInspectionClient{source: source, receipt: api.CommitReceiptResponse{OperationID: uuid.NewString()}, operationErr: errors.New("store unavailable")}, "accepted", "unknown", "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := inspectCommitEvent(t.Context(), &tc.client, source.ID, event, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if tc.acceptance != "" && (out.Acceptance != tc.acceptance || out.Execution != tc.execution || out.DatabaseCommit != tc.commit) {
				t.Fatalf("facts=%+v", out)
			}
		})
	}
	source.RelayStatus = "blocked_events"
	c := &commitInspectionClient{source: source, receiptErr: missing, blocked: api.CommitBlockedEventsResponse{Items: []api.CommitBlockedEventResponse{{EventID: event, ObservedAt: now}}}}
	out, err := inspectCommitEvent(t.Context(), c, source.ID, event, now)
	if err != nil || out.Acceptance != "blocked" || out.DatabaseCommit != "observed" || out.Action == "" {
		t.Fatalf("fresh blocked event=%+v err=%v", out, err)
	}
}

func TestCommitWaitTimeoutAndTerminalStates(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled", "expired"} {
		t.Run(state, func(t *testing.T) {
			client := &commitInspectionClient{receipt: api.CommitReceiptResponse{OperationID: uuid.NewString()}, operation: api.CommitOperationResponse{State: state}}
			out, err := waitCommitEvent(t.Context(), client, "source", "event", "completed", time.Hour)
			if err != nil || out.Execution != state || client.reads != 1 {
				t.Fatalf("terminal=%+v reads=%d err=%v", out, client.reads, err)
			}
		})
	}
	client := &commitInspectionClient{receipt: api.CommitReceiptResponse{OperationID: uuid.NewString()}, operation: api.CommitOperationResponse{State: "pending"}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	out, err := waitCommitEvent(ctx, client, "source", "event", "completed", time.Hour)
	if !errors.Is(err, context.DeadlineExceeded) || out.Execution != "pending" {
		t.Fatalf("timeout lost last facts: %+v err=%v", out, err)
	}
	out, err = waitCommitEvent(t.Context(), client, "source", "event", "accepted", time.Hour)
	if err != nil || out.Acceptance != "accepted" {
		t.Fatalf("accepted wait=%+v err=%v", out, err)
	}
	client.operationErr = errors.New("execution status unavailable")
	out, err = waitCommitEvent(t.Context(), client, "source", "event", "accepted", time.Hour)
	if err != nil || out.Acceptance != "accepted" || out.Execution != "unknown" || out.Receipt == nil {
		t.Fatalf("execution read failure hid accepted work: %+v err=%v", out, err)
	}
	out, err = waitCommitEvent(t.Context(), client, "source", "event", "completed", time.Hour)
	if err == nil || out.Acceptance != "accepted" || out.Execution != "unknown" || out.Receipt == nil {
		t.Fatalf("failed completion read lost acceptance or claimed completion: %+v err=%v", out, err)
	}
	client.receiptErr = &APIError{Problem: api.Problem{Status: http.StatusServiceUnavailable, Code: "capacity"}}
	out, err = waitCommitEvent(t.Context(), client, "source", "event", "completed", time.Hour)
	if err == nil || out.EventID != "event" || out.Acceptance != "unknown" || out.Execution != "unknown" || out.DatabaseCommit != "unknown" {
		t.Fatalf("initial read failure did not preserve unknown facts: %+v err=%v", out, err)
	}
}

func TestCommitDoctorStaleAndPausedSources(t *testing.T) {
	now := time.Now().UTC()
	stale, future := now.Add(-10*time.Minute), now.Add(10*time.Minute)
	for _, tc := range []struct {
		name             string
		checked          *time.Time
		enabled          bool
		status, expected string
	}{
		{"fresh", &now, true, "healthy", "pass"},
		{"absent", nil, true, "healthy", "unknown"},
		{"stale", &stale, true, "healthy", "unknown"},
		{"future clock", &future, true, "healthy", "unknown"},
		{"outage", &now, true, "database_unavailable", "fail"},
		{"paused", &now, false, "healthy", "pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := diagnoseCommitSource(api.CommitSourceResponse{Enabled: tc.enabled, OperationPolicy: "orders", LastCheckedAt: tc.checked, RelayStatus: tc.status}, now)
			if out.Checks[2].State != tc.expected {
				t.Fatalf("checks=%+v", out.Checks)
			}
			if !tc.enabled && out.Checks[0].State != "fail" {
				t.Fatal("paused source passed")
			}
		})
	}
}

func TestCommitDoctorLocalCredentialIsNotUploadedOrPrinted(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"00000000-0000-4000-8000-000000000001","enabled":true,"operation_policy":"orders"}`, http.StatusOK)
	out, restore := swapStdout(t)
	defer restore()
	file := filepath.Join(t.TempDir(), "credential")
	secret := "postgres://relay:private-doctor-password@db.example/customer?sslmode=disable"
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdCommit([]string{"doctor", "00000000-0000-4000-8000-000000000001", "--file", file}); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/commit-sources/00000000-0000-4000-8000-000000000001" {
		t.Fatalf("doctor mutated source: %s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(out.String(), "private-doctor-password") || strings.Contains(out.String(), "postgres://") {
		t.Fatal("doctor leaked credential")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	f.sawMethod = ""
	if code := cmdCommit([]string{"doctor", uuid.NewString(), "--file", link}); code == 0 || f.sawMethod != "" {
		t.Fatal("doctor accepted a symlink or contacted API before rejecting it")
	}
}
