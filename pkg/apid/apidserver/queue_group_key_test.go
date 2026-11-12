// adr: 20261112193306
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestQueueGroupKeyOrdersPullDelivery(t *testing.T) {
	if api.QueueOrderedWorkPolicy != workpolicy.Ordered {
		t.Fatalf("api %q and workpolicy %q name different built-in policies", api.QueueOrderedWorkPolicy, workpolicy.Ordered)
	}
	e := apidserver.Setup(t, api.PlanPro)
	queueLeaseApp(t, e, "group-worker", 0)
	send := func(payload, key string) *http.Response {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/send",
			api.QueueSendRequest{QueueName: "jobs", Payload: json.RawMessage(payload), GroupKey: key}, nil)
		return rec.Result()
	}
	if res := send(`{"n":1}`, "order-42"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("unreleased group_key = %d", res.StatusCode)
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.QueueMessageGroups)

	for _, tc := range []struct{ payload, key string }{{`{"n":1}`, "order-42"}, {`{"n":2}`, "order-42"}, {`{"n":3}`, "order-7"}} {
		if res := send(tc.payload, tc.key); res.StatusCode != http.StatusCreated {
			t.Fatalf("send %s = %d", tc.payload, res.StatusCode)
		}
	}
	batch := e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/send-batch", api.QueueSendBatchRequest{QueueName: "jobs",
		Messages: []api.QueueSendBatchMessage{{Payload: json.RawMessage(`{"n":4}`), GroupKey: "order-42"}, {Payload: json.RawMessage(`{"n":5}`)}}}, nil)
	if batch.Code != http.StatusOK || strings.Contains(batch.Body.String(), "rejected") {
		t.Fatalf("batch = %d: %s", batch.Code, batch.Body.String())
	}

	lease := func() ([]string, []string) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/jobs/leases", api.QueueLeaseRequest{MaxMessages: 10, VisibilityTimeoutSeconds: 60}, nil)
		var out api.QueueLeaseResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("lease = %d: %s", rec.Code, rec.Body.String())
		}
		var payloads, receipts []string
		for _, m := range out.Messages {
			payloads = append(payloads, string(m.Payload))
			receipts = append(receipts, m.Receipt)
		}
		return payloads, receipts
	}
	ack := func(receipts []string) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/jobs/leases:ack", api.QueueLeaseReceiptsRequest{Receipts: receipts}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("ack = %d: %s", rec.Code, rec.Body.String())
		}
	}
	got, receipts := lease()
	slices.Sort(got)
	if want := []string{`{"n":1}`, `{"n":3}`, `{"n":5}`}; !slices.Equal(got, want) {
		t.Fatalf("first lease %v, want one per key plus the unkeyed message %v", got, want)
	}
	ack(receipts)
	for _, want := range []string{`{"n":2}`, `{"n":4}`} {
		got, receipts = lease()
		if !slices.Equal(got, []string{want}) {
			t.Fatalf("lease %v, want %s next for order-42", got, want)
		}
		ack(receipts)
	}

	// group_key is shorthand for the built-in policy, so not combinable with
	// work; its name is reserved for declared policies.
	rec := e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/send", api.QueueSendRequest{QueueName: "jobs", Payload: json.RawMessage(`{}`),
		GroupKey: "k", Work: &api.InvokeWork{Policy: "other", Key: json.RawMessage(`"k"`)}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.Do(t, http.MethodPost, "/v1/apps/group-worker/queues/send", api.QueueSendRequest{QueueName: "jobs", Payload: json.RawMessage(`{}`),
		GroupKey: strings.Repeat("k", api.QueueGroupKeyMaxBytes+1)}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.Do(t, http.MethodPut, "/v1/apps/group-worker/work-policies/gregale-mine", api.UpsertWorkPolicyRequest{MaxRunningPerKey: 1}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
