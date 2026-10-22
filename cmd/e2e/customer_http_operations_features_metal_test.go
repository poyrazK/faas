//go:build metal

// adr: 20261018161833
package e2e

// Native qualification of the gated Operations features through the real
// daemons and guest: schedules (ADR-20261018161833), child Operations
// (ADR-20261018183000) and per-customer concurrency (ADR-20261018210500).
// Each test opens its feature to the fixture account only, the way an
// operator would before a production test.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// openOperationFeatures adds the account to every Operations feature
// allowlist and restarts apid so the gates and the background loops see it.
func openOperationFeatures(t *testing.T, h *e2etest.Harness, accountID string) {
	t.Helper()
	for _, key := range []string{"FAAS_OPERATION_SCHEDULES_ACCOUNTS", "FAAS_OPERATION_CHILDREN_ACCOUNTS",
		"FAAS_OPERATION_CONCURRENCY_ACCOUNTS", "FAAS_OPERATION_APPROVAL_INPUTS_ACCOUNTS"} {
		if err := h.SetAPIDEnv(key, accountID); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
}

// gatedJSON calls a gated route the SDK does not carry and decodes the reply.
func gatedJSON(t *testing.T, client *api.Client, method, path string, body, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, client.BaseURL()+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token())
	request.Header.Set("Content-Type", "application/json")
	response, err := client.HTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if out != nil && response.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	if response.StatusCode >= 400 && out != nil {
		t.Logf("%s %s: HTTP %d %s", method, path, response.StatusCode, raw)
	}
	return response.StatusCode
}

func mustGatedJSON(t *testing.T, client *api.Client, method, path string, body, out any, want int) {
	t.Helper()
	if got := gatedJSON(t, client, method, path, body, out); got != want {
		t.Fatalf("%s %s: HTTP %d, want %d", method, path, got, want)
	}
}

// A once schedule fires through apid's firer and its Operation runs on the
// real guest; the schedule records the admitted Operation.
func TestCustomerHTTPOperationScheduleFiresMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	openOperationFeatures(t, f.h, f.account.ID)
	at := time.Now().Add(20 * time.Second)
	var schedule api.OperationScheduleResponse
	mustGatedJSON(t, f.customer, http.MethodPost, "/v1/platform-tenant-self/customer-operation-schedules",
		api.OperationScheduleRequest{DefinitionID: f.definition.ID, At: &at, Input: json.RawMessage(`{"mode":"complete"}`)}, &schedule, http.StatusCreated)
	if schedule.State != api.OperationScheduleActive || schedule.NextFireAt == nil {
		t.Fatalf("created schedule %+v", schedule)
	}
	customerHTTPEventually(t, 3*time.Minute, "schedule fire", func() bool {
		gatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operation-schedules/"+schedule.ID, nil, &schedule)
		return schedule.State == api.OperationScheduleCompleted || schedule.State == api.OperationScheduleFailed
	})
	if schedule.State != api.OperationScheduleCompleted || schedule.Fires != 1 || schedule.LastOperationID == "" || schedule.LastError != "" {
		t.Fatalf("fired schedule %+v", schedule)
	}
	f.mu.Lock()
	f.operationIDs[schedule.LastOperationID] = true
	f.mu.Unlock()
	op := f.waitState(t, schedule.LastOperationID, api.OperationSucceeded)
	var result struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(op.Result, &result); err != nil || result.Version != "original" {
		t.Fatalf("scheduled operation result %s: %v", op.Result, err)
	}
}

// A parent on the real guest starts a child with its own attempt proof; the
// replayed start returns the same child, which runs to success and is listed
// for the customer and the account.
func TestCustomerHTTPOperationChildrenMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	openOperationFeatures(t, f.h, f.account.ID)
	parent := f.start(t, "fanout", "fanout-1")
	f.waitState(t, parent, api.OperationSucceeded)
	var children api.OperationChildrenResponse
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operations/"+parent+"/children", nil, &children, http.StatusOK)
	if len(children.Children) != 1 || children.Children[0].Key != "child-1" {
		t.Fatalf("customer children %+v", children)
	}
	child := children.Children[0].Operation.ID
	f.mu.Lock()
	f.operationIDs[child] = true
	f.mu.Unlock()
	f.waitState(t, child, api.OperationSucceeded)
	var owned api.OperationChildrenResponse
	mustGatedJSON(t, f.owner, http.MethodGet, fmt.Sprintf("/v1/apps/%s/operations/%s/children", f.app.Slug, parent), nil, &owned, http.StatusOK)
	if len(owned.Children) != 1 || owned.Children[0].Operation.ID != child {
		t.Fatalf("account children %+v", owned)
	}
	// A child cannot start children: its own listing is empty and stays so.
	mustGatedJSON(t, f.owner, http.MethodGet, fmt.Sprintf("/v1/apps/%s/operations/%s/children", f.app.Slug, child), nil, &owned, http.StatusOK)
	if len(owned.Children) != 0 {
		t.Fatalf("child listed children %+v", owned)
	}
}

// With one slot per customer, a second Operation queues behind a held one,
// shows its queue position, and starts on the real guest once the first
// finishes.
func TestCustomerHTTPOperationConcurrencyQueuesPerCustomerMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	openOperationFeatures(t, f.h, f.account.ID)
	mustGatedJSON(t, f.owner, http.MethodPut, "/v1/apps/"+f.app.Slug+"/operation-concurrency",
		api.OperationConcurrencyPolicyRequest{Operation: "export", MaxRunning: 1}, &api.OperationConcurrencyPolicy{}, http.StatusCreated)

	first := f.start(t, "hold", "slot-1")
	customerHTTPEventually(t, 3*time.Minute, "first operation entered the guest", func() bool {
		return len(f.relay.observation(first).entered) > 0
	})
	second := f.start(t, "complete", "slot-2")
	var queue api.OperationQueueResponse
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operation-queue", nil, &queue, http.StatusOK)
	if len(queue.Queued) != 1 || queue.Queued[0].OperationID != second || queue.Queued[0].Position != 1 {
		t.Fatalf("customer queue %+v", queue)
	}
	// Held well past the releaser's two-second pass, the second stays queued.
	time.Sleep(6 * time.Second)
	if op := f.read(t, second); op.State != api.OperationAccepted || len(f.relay.observation(second).entered) != 0 {
		t.Fatalf("queued operation ran early: %s entered=%v", op.State, f.relay.observation(second).entered)
	}
	var usage api.OperationConcurrencyResponse
	mustGatedJSON(t, f.owner, http.MethodGet, "/v1/apps/"+f.app.Slug+"/operation-concurrency", nil, &usage, http.StatusOK)
	if len(usage.Queues) != 1 || usage.Queues[0].Limit != 1 || usage.Queues[0].InFlight != 1 || usage.Queues[0].Queued != 1 {
		t.Fatalf("account usage %+v", usage)
	}

	f.relay.direct(first, customerHTTPRelease)
	f.waitState(t, first, api.OperationSucceeded)
	f.waitState(t, second, api.OperationSucceeded)
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operation-queue", nil, &queue, http.StatusOK)
	if len(queue.Queued) != 0 {
		t.Fatalf("queue after release %+v", queue)
	}
}
