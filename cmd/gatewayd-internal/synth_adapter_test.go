package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type replayTestBackend struct {
	rule          gateway.MirrorRuleRow
	target        gateway.Target
	scheduleCalls atomic.Int32
}

func (b *replayTestBackend) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{ID: b.rule.AppID, AccountID: b.rule.AccountID}, true
}

func (b *replayTestBackend) Pick(string) gateway.PickResult {
	return gateway.PickResult{Target: b.target, OK: true, Picked: b.target.DeploymentID}
}

func (*replayTestBackend) HealthyCount(string) int { return 1 }

func (*replayTestBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	return "wake-source", gateway.WakeMethodColdBoot, false, nil
}

func (b *replayTestBackend) LookupMirrorRules(context.Context, string) ([]gateway.MirrorRuleRow, bool) {
	return []gateway.MirrorRuleRow{b.rule}, true
}

func (b *replayTestBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "legacy-instance", "legacy-wake", nil
}

func (b *replayTestBackend) LookupMirrorRuleForReplay(context.Context, string, string) (gateway.MirrorRuleRow, bool, error) {
	return b.rule, true, nil
}

func (b *replayTestBackend) ScheduleMirrorTarget(context.Context, string, string, string) (gateway.Target, error) {
	b.scheduleCalls.Add(1)
	return b.target, nil
}

func TestSynthAdapterForwardInvocationStampsPlatformHeaders(t *testing.T) {
	a := &synthAdapter{
		forward: func(target gateway.Target) http.Handler {
			if target.InstanceID != "instance-1" || target.NodeID != "node-1" {
				t.Fatalf("target = %#v, want instance/node target", target)
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				for key, want := range map[string]string{
					"x-faas-invocation-id":     "inv-1",
					"x-faas-app-id":            "app-1",
					"x-faas-invocation-source": "async_invoke",
					"x-faas-instance":          "instance-1",
					"x-faas-node":              "node-1",
				} {
					if got := r.Header.Get(key); got != want {
						t.Errorf("%s = %q, want %q", key, got, want)
					}
				}
				if string(body) != `{"hello":"world"}` {
					t.Errorf("body = %q, want request payload", body)
				}
				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
		},
	}
	out, err := a.forwardInvocation(context.Background(), gateway.Target{
		InstanceID: "instance-1",
		NodeID:     "node-1",
	}, state.Invocation{
		ID:      "inv-1",
		AppID:   "app-1",
		Source:  state.InvocationAsyncInvoke,
		Method:  http.MethodPost,
		Path:    "/e2e",
		Payload: []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatalf("forwardInvocation: %v", err)
	}
	if string(out.Result) != `{"ok":true}` {
		t.Fatalf("result = %s, want response body", out.Result)
	}
	if out.State != state.InvocationDispatching {
		t.Fatalf("state = %q, want dispatching", out.State)
	}
}

func TestSynthAdapterForwardInvocationMarksHandlerErrorFailed(t *testing.T) {
	a := &synthAdapter{forward: func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"handler_error","message":"boom"}`))
		})
	}}
	out, statusCode, err := a.forwardInvocationWithStatus(context.Background(), gateway.Target{
		InstanceID: "instance-1", NodeID: "node-1",
	}, state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke})
	if err != nil {
		t.Fatalf("forwardInvocationWithStatus: %v", err)
	}
	if statusCode != http.StatusInternalServerError || out.State != state.InvocationFailed {
		t.Fatalf("status/state = %d/%q, want 500/failed", statusCode, out.State)
	}
	if !strings.Contains(string(out.Result), "handler_error") {
		t.Fatalf("result = %s", out.Result)
	}
}

func TestSynthAdapterForwardInvocationKeepsOrdinaryServerErrorRetryable(t *testing.T) {
	a := &synthAdapter{forward: func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"upstream_unavailable"}`))
		})
	}}
	out, statusCode, err := a.InvokeWithTargetStatus(context.Background(), "app-1", state.Invocation{
		ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke,
	}, gateway.Target{InstanceID: "instance-1", NodeID: "node-1"})
	if err != nil {
		t.Fatalf("InvokeWithTargetStatus: %v", err)
	}
	if statusCode != http.StatusServiceUnavailable || out.State != state.InvocationDispatching {
		t.Fatalf("status/state = %d/%q, want 503/dispatching", statusCode, out.State)
	}
}

func TestSynthAdapterDebugReplayUsesMirrorTargetAndStripsMetadata(t *testing.T) {
	b := &replayTestBackend{
		rule: gateway.MirrorRuleRow{
			ID:                 "rule-1",
			AccountID:          "acct-1",
			AppID:              "app-1",
			SourceDeploymentID: "dep-source",
			MirrorDeploymentID: "dep-mirror",
			Enabled:            true,
		},
		target: gateway.Target{NodeID: "node-mirror", InstanceID: "instance-mirror", DeploymentID: "dep-mirror"},
	}
	metadata, err := json.Marshal(map[string]string{
		api.DebugReplayRequestIDHeader:     "request-1",
		api.DebugReplayDeploymentIDHeader:  "dep-source",
		api.DebugReplayMirrorRuleIDHeader:  "rule-1",
		api.DebugReplaySourceStatusHeader:  "200",
		api.DebugReplaySourceLatencyHeader: "12",
		api.DebugReplayTraceIDHeader:       "trace-1",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	a := &synthAdapter{
		backend: b,
		forward: func(target gateway.Target) http.Handler {
			if target != b.target {
				t.Fatalf("target = %#v, want mirror target %#v", target, b.target)
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if len(body) != 0 {
					t.Errorf("replay body = %q, want empty body", body)
				}
				for _, key := range []string{
					api.DebugReplayRequestIDHeader,
					api.DebugReplayDeploymentIDHeader,
					api.DebugReplayMirrorRuleIDHeader,
				} {
					if got := r.Header.Get(key); got != "" {
						t.Errorf("replay leaked %s=%q to mirror", key, got)
					}
				}
				if got := r.Header.Get("X-Trace-Id"); got != "trace-1" {
					t.Errorf("trace header = %q, want retained trace id", got)
				}
				w.WriteHeader(http.StatusOK)
			})
		},
	}
	out, status, err := a.InvokeWithStatus(context.Background(), "app-1", state.Invocation{
		ID:        "inv-1",
		AppID:     "app-1",
		AccountID: "acct-1",
		Source:    state.InvocationReplay,
		Method:    http.MethodPost,
		Path:      "/replayed",
		Payload:   []byte(`{"sensitive":"discard"}`),
		Headers:   metadata,
	})
	if err != nil {
		t.Fatalf("InvokeWithStatus: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if b.scheduleCalls.Load() != 1 {
		t.Fatalf("ScheduleMirrorTarget calls = %d, want 1", b.scheduleCalls.Load())
	}
	var result struct {
		SourceStatusCode int  `json:"source_status_code"`
		MirrorStatusCode int  `json:"mirror_status_code"`
		StatusDiff       bool `json:"status_diff"`
		Crashed          bool `json:"crashed"`
	}
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("decode replay result %q: %v", out.Result, err)
	}
	if result.SourceStatusCode != 200 || result.MirrorStatusCode != 200 || result.StatusDiff || result.Crashed {
		t.Fatalf("replay result = %+v, want matching healthy statuses", result)
	}
}
