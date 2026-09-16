package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type normalPathAdmissionResult struct {
	status  int
	headers http.Header
	body    []byte
	err     error
}

func normalPathAdmissionRequest(client *http.Client, ctx context.Context, gatewayURL, host, path string) <-chan normalPathAdmissionResult {
	result := make(chan normalPathAdmissionResult, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+path, nil)
		if err != nil {
			result <- normalPathAdmissionResult{err: err}
			return
		}
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			result <- normalPathAdmissionResult{err: err}
			return
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		result <- normalPathAdmissionResult{
			status:  resp.StatusCode,
			headers: resp.Header,
			body:    body,
			err:     readErr,
		}
	}()
	return result
}

// TestE2E_NormalPath_CancelledQueuedAdmissionReleasesCapacity covers the
// request-side cancellation gap between the unit-level concurrency manager
// and the real HTTP path. A canceled request waiting behind a full instance
// must never reach VMMD, and the same instance must still accept a later
// request after the active streams release their slots.
func TestE2E_NormalPath_CancelledQueuedAdmissionReleasesCapacity(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "normal-admission-cancel", api.PlanFree)
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, f.app.ID, f.nodeID, "admission-cancel")
	f.vmmd.SetVersion(instance.ID, "admission-cancel")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:admission-cancel\n", 10*time.Second)

	slotCount := api.MustLimitsFor(api.PlanFree).ConcurrencyPerVMBound
	gate := f.vmmd.InstallRequestGate(instance.ID, slotCount)
	defer gate.Release()
	client := f.h.HTTPClient()

	active := make([]<-chan normalPathAdmissionResult, 0, slotCount)
	for i := 0; i < slotCount; i++ {
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		active = append(active, normalPathAdmissionRequest(client, ctx, f.h.GatewayURL, f.host,
			fmt.Sprintf("/admission-cancel/active/%d", i)))
	}
	if !gate.WaitArrived(5 * time.Second) {
		t.Fatal("active requests did not fill the instance slots")
	}

	waiterCtx, cancelWaiter := context.WithCancel(f.ctx)
	waiter := normalPathAdmissionRequest(client, waiterCtx, f.h.GatewayURL, f.host, "/admission-cancel/waiter")
	time.Sleep(150 * time.Millisecond)
	cancelWaiter()
	select {
	case got := <-waiter:
		if got.err == nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled queued request err=%v, want context.Canceled", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled queued request did not terminate")
	}

	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == "/admission-cancel/waiter" {
			t.Fatal("canceled queued request reached VMMD")
		}
	}

	gate.Release()
	for i, resultCh := range active {
		select {
		case got := <-resultCh:
			if got.err != nil || got.status != http.StatusOK || string(got.body) != "normal-path:admission-cancel\n" {
				t.Errorf("active request %d response=(status=%d,body=%q,err=%v), want 200", i, got.status, got.body, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("active request %d did not complete after releasing the instance slots", i)
		}
	}

	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	after := normalPathAdmissionRequest(client, ctx, f.h.GatewayURL, f.host, "/admission-cancel/after")
	select {
	case got := <-after:
		if got.err != nil || got.status != http.StatusOK || string(got.body) != "normal-path:admission-cancel\n" {
			t.Fatalf("request after cancellation response=(status=%d,body=%q,err=%v), want 200", got.status, got.body, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request after cancellation did not complete")
	}
	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == "/admission-cancel/after" {
			return
		}
	}
	t.Fatal("request after cancellation did not reach VMMD")
}

// TestE2E_NormalPath_QueuedAdmissionDoesNotConsumeExecutionBudget proves that
// the customer execution budget starts after per-VM capacity admission. A
// request can wait behind a full instance for longer than its configured
// budget and still receive the full budget once it enters the guest bridge.
func TestE2E_NormalPath_QueuedAdmissionDoesNotConsumeExecutionBudget(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "normal-admission-timeout", api.PlanFree)
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, f.app.ID, f.nodeID, "admission-timeout")
	f.vmmd.SetVersion(instance.ID, "admission-timeout")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:admission-timeout\n", 10*time.Second)

	slotCount := api.MustLimitsFor(api.PlanFree).ConcurrencyPerVMBound
	gate := f.vmmd.InstallRequestGate(instance.ID, slotCount)
	defer gate.Release()
	client := f.h.HTTPClient()
	active := make([]<-chan normalPathAdmissionResult, 0, slotCount)
	for i := 0; i < slotCount; i++ {
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		active = append(active, normalPathAdmissionRequest(client, ctx, f.h.GatewayURL, f.host,
			fmt.Sprintf("/admission-timeout/active/%d", i)))
	}
	if !gate.WaitArrived(5 * time.Second) {
		t.Fatal("active requests did not fill the instance slots")
	}

	accountID := accountIDFromKey(t, f.ctx, f.h.Pool, f.key)
	seedEdgeRuleDirect(t, f.ctx, f.h.Pool, accountID, f.app.ID, f.host,
		state.EdgeRuleKindBudget,
		map[string]any{
			"kind": "budget",
			"budget": map[string]any{
				"budget_ms": 500,
			},
		})
	resetEdgeRuleCache(t, f.h)

	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	waiter := normalPathAdmissionRequest(client, ctx, f.h.GatewayURL, f.host, "/admission-timeout/waiter")
	select {
	case got := <-waiter:
		t.Fatalf("queued request completed before capacity was released: status=%d body=%q err=%v", got.status, got.body, got.err)
	case <-time.After(750 * time.Millisecond):
		// The request has now waited longer than the 500 ms execution budget.
	}

	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == "/admission-timeout/waiter" {
			t.Fatal("queued request reached VMMD before capacity was released")
		}
	}

	gate.Release()
	select {
	case got := <-waiter:
		if got.err != nil {
			t.Fatalf("queued request failed after capacity release: %v", got.err)
		}
		if got.status != http.StatusOK || string(got.body) != "normal-path:admission-timeout\n" {
			t.Fatalf("queued request response=(status=%d,body=%q), want 200 after capacity release", got.status, got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued request did not complete after capacity release")
	}

	foundWaiter := false
	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == "/admission-timeout/waiter" {
			foundWaiter = true
			break
		}
	}
	if !foundWaiter {
		t.Fatal("queued request did not reach VMMD after capacity release")
	}
	for i, resultCh := range active {
		select {
		case got := <-resultCh:
			if got.err != nil || got.status != http.StatusOK || string(got.body) != "normal-path:admission-timeout\n" {
				t.Errorf("active request %d response=(status=%d,body=%q,err=%v), want 200", i, got.status, got.body, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("active request %d did not complete after releasing the instance slots", i)
		}
	}
}
