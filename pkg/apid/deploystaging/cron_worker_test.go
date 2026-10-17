package deploystaging

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type cronWorkerFixture struct{ calls int }

func (f *cronWorkerFixture) ReconcileDeploymentCrons(context.Context) ([]state.CronActivationChange, error) {
	f.calls++
	return []state.CronActivationChange{{AccountID: "account", AppID: "app"}}, nil
}
func TestCronActivationWorkerRunsWithoutCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &cronWorkerFixture{}
	notified := 0
	RunCronActivationWorker(ctx, store, func(_ context.Context, accountID, appID, operation string) {
		if accountID != "account" || appID != "app" || operation != "activate" {
			t.Fatalf("change=%s %s %s", accountID, appID, operation)
		}
		notified++
		cancel()
	}, func() { t.Fatal("unexpected worker failure") })
	if store.calls != 1 || notified != 1 {
		t.Fatalf("calls=%d notifications=%d", store.calls, notified)
	}
}
