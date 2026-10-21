//go:build metal

// adr: 20261018183000
package e2e

// Native qualification of the job Operations follow-ups: a job parent starts
// a child with its run proof (ADR-20261018183000), and a job Operation
// queues behind a per-customer concurrency limit (ADR-20261018210500).

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A fanout job on the real guest starts one child with its run proof and
// replays it by key; the child job then runs to success on its own guest.
func TestCustomerJobOperationChildrenMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	openOperationFeatures(t, f.h.Harness, f.account.ID)
	parent := f.start(t, "fanout", "fanout-export")
	f.connect(t, parent, false)
	f.waitState(t, parent, api.OperationSucceeded)
	var children api.OperationChildrenResponse
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operations/"+parent+"/children", nil, &children, http.StatusOK)
	if len(children.Children) != 1 || children.Children[0].Key != "child-1" {
		t.Fatalf("customer children %+v", children)
	}
	child := children.Children[0].Operation.ID
	f.operationIDs = append(f.operationIDs, child)
	f.connect(t, child, false)
	f.waitState(t, child, api.OperationSucceeded)
	var owned api.OperationChildrenResponse
	mustGatedJSON(t, f.owner, http.MethodGet, fmt.Sprintf("/v1/apps/%s/operations/%s/children", f.app.Slug, parent), nil, &owned, http.StatusOK)
	if len(owned.Children) != 1 || owned.Children[0].Operation.ID != child {
		t.Fatalf("account children %+v", owned)
	}
	f.waitNoInstances(t)
}

// With one slot per customer, a second job Operation queues behind a held
// one: its task is not claimed while the first runs, and it runs on the
// real guest once the first finishes.
func TestCustomerJobOperationConcurrencyQueuesMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	openOperationFeatures(t, f.h.Harness, f.account.ID)
	mustGatedJSON(t, f.owner, http.MethodPut, "/v1/apps/"+f.app.Slug+"/operation-concurrency",
		api.OperationConcurrencyPolicyRequest{Operation: "export", MaxRunning: 1}, &api.OperationConcurrencyPolicy{}, http.StatusCreated)
	first := f.start(t, "hold", "slot-1")
	relay, _ := f.connect(t, first, false)
	f.waitProgress(t, first)
	second := f.start(t, "complete", "slot-2")
	var queue api.OperationQueueResponse
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operation-queue", nil, &queue, http.StatusOK)
	if len(queue.Queued) != 1 || queue.Queued[0].OperationID != second || queue.Queued[0].Position != 1 {
		t.Fatalf("customer queue %+v", queue)
	}
	// Held well past the releaser's two-second pass, the second stays queued
	// and no instance claims its task.
	time.Sleep(6 * time.Second)
	op, err := f.store.OperationByID(context.Background(), f.account.ID, f.tenant.ID, second)
	mustCustomerJob(t, err)
	if op.State != api.OperationAccepted {
		t.Fatalf("queued job operation is %s", op.State)
	}
	if op.JobRunID != "" {
		if task, err := f.store.JobTaskGet(context.Background(), op.JobRunID, 0); err == nil && task.InstanceID != nil {
			t.Fatal("queued job operation's task was claimed")
		}
	}
	relay.release.Store(true)
	f.waitState(t, first, api.OperationSucceeded)
	f.connect(t, second, false)
	f.waitState(t, second, api.OperationSucceeded)
	mustGatedJSON(t, f.customer, http.MethodGet, "/v1/platform-tenant-self/customer-operation-queue", nil, &queue, http.StatusOK)
	if len(queue.Queued) != 0 {
		t.Fatalf("queue after release %+v", queue)
	}
	f.waitNoInstances(t)
}
