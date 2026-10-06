// adr: 610
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeActivityNodeCacheWrapsBothFactoriesIncludingFailures(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "raw"}[raw], func(t *testing.T) {
			tracker, err := activity.New(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			cache := (&nodeCache{cache: gateway.NewNodeClientCache(nil, nil)}).WithActivityTracker(tracker)
			defer func() { _ = cache.Close() }()
			target := gateway.Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString()}
			factory := cache.Forwarding()
			if raw {
				factory = cache.RawForwarding()
			}
			writer := httptest.NewRecorder()
			factory(target).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := tracker.Observe(target.AppID, target.DeploymentID); got.ActiveForwards != 0 || !got.CoverageKnown || got.ActivityVersion != 3 {
				t.Fatalf("bridge failure bypassed activity completion: %+v", got)
			}
			if writer.Code != http.StatusServiceUnavailable {
				t.Fatalf("node-unavailable response changed: %d", writer.Code)
			}
		})
	}
}

func TestRuntimeActivitySyntheticInvocationUsesCommonForwarding(t *testing.T) {
	tracker, err := activity.New(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := gateway.Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), NodeID: uuid.NewString()}
	for _, source := range []state.InvocationSource{state.InvocationAsyncInvoke, state.InvocationQueue, state.InvocationDelayedTask, state.InvocationCron} {
		adapter := &synthAdapter{forward: gateway.WithDeploymentActivity(func(gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
					t.Errorf("synthetic forward was not counted: %+v", got)
				}
				w.WriteHeader(http.StatusNoContent)
			})
		}, tracker)}
		_, err := adapter.forwardInvocation(context.Background(), target, state.Invocation{AppID: target.AppID, ID: uuid.NewString(), Source: source, Method: http.MethodPost, Path: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 0 {
			t.Fatalf("synthetic completion retained activity: %+v", got)
		}
	}
}
