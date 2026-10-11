package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #4 (H4-22): the sync invoke wait equalled the SDK's
// 30 s client timeout, so the CLI always failed first with "Could not reach
// Gregale" while the invocation was failing on a scan_critical wake.
func TestSyncInvokeWaitStaysInsideTheClientTimeout(t *testing.T) {
	for _, seconds := range []int{api.SyncInvokeWaitSeconds, api.SyncInvokeWaitSecondsFree} {
		if wait := time.Duration(seconds) * time.Second; wait <= 0 || wait+3*time.Second > api.DefaultClientTimeout {
			t.Fatalf("sync invoke wait %s must leave headroom under the %s client timeout", wait, api.DefaultClientTimeout)
		}
	}
}

func TestSyncInvokeTimeoutProblemNamesTheInvocation(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "slow-api")
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AccountID: e.acct.ID, AppID: appID, Source: state.InvocationAsyncInvoke, State: state.InvocationPending,
		Method: "POST", Path: "/", DueAt: time.Now(), CreatedAt: time.Now(),
		LastError: "wake failed: scan_critical: Fixable CRITICAL vulnerability in base ext4",
	})
	if err != nil {
		t.Fatal(err)
	}
	p := e.s.syncInvokeTimeoutProblem(context.Background(), inv, 25*time.Second)
	if p.Status != 504 || p.Code != "long_poll_timeout" {
		t.Fatalf("problem = %d %s, want 504 long_poll_timeout", p.Status, p.Code)
	}
	for _, want := range []string{inv.ID, "after 25s", "scan_critical", "gregale invocations get " + inv.ID} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail %q missing %q", p.Detail, want)
		}
	}
}
