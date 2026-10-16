package mcpmanagement

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func activityAdmissionArgs(consumer bool, wait int) (string, map[string]any) {
	if consumer {
		return watchActivityConsumerTool, consumerWatchArgs("worker", 1, wait)
	}
	args := activityArgs("", 1)
	args["wait_seconds"] = wait
	return "gregale_watch_environment_cache_schedule_activity", args
}

func TestAdmissionActivityWatchesSharePoolAndPreserveAckCapacity(t *testing.T) {
	for _, httpTransport := range []bool{false, true} {
		t.Run(fmt.Sprintf("http_%v", httpTransport), func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			opts.Telemetry = NewTelemetry()
			var armed atomic.Bool
			var feedCalls, nativeCalls atomic.Int64
			entered := make(chan struct{}, maxActivityWatches)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nativeCalls.Add(1)
				if armed.Load() && strings.HasSuffix(r.URL.Path, "/activity") && feedCalls.Add(1) <= int64(maxActivityWatches) {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", httpTransport)
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			callCtx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			armed.Store(true)
			done := make(chan struct{}, maxActivityWatches)
			for i := range maxActivityWatches {
				name, args := activityAdmissionArgs(i%2 == 1, 5)
				go func() {
					defer func() { done <- struct{}{} }()
					_, _ = h.client.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: args})
				}()
			}
			for range maxActivityWatches {
				select {
				case <-entered:
				case <-h.ctx.Done():
					t.Fatal("watches never occupied pool")
				}
			}
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 2`)
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="short_requests"} 0`)
			// Another SDK session on this same management instance must share the pool.
			ct, st := mcp.NewInMemoryTransports()
			ss, err := h.manager.resourceServer.Connect(h.ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ss.Close() })
			other, err := mcp.NewClient(&mcp.Implementation{Name: "activity-admission-second", Version: "1"}, nil).Connect(h.ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			for _, consumer := range []bool{false, true} {
				name, args := activityAdmissionArgs(consumer, 1)
				before := nativeCalls.Load()
				result, err := other.CallTool(h.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
				assertBusyTool(t, result, err)
				if nativeCalls.Load() != before {
					t.Fatal("rejected watch performed native I/O")
				}
				expectMetric(t, opts.Telemetry, `gregale_management_mcp_requests_total{operation="tools/call",outcome="busy",tool="`+name+`"} 1`)
			}
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_admission_rejections_total{kind="activity_watches"} 2`)
			// ACK validation includes native feed reads. It must remain available while
			// both observation slots are held, as must exact consumer and metadata reads.
			ack := decodeConsumer(t, callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 1, false), false))
			if ack.Revision != 1 {
				t.Fatal(ack)
			}
			got := decodeConsumer(t, callTool(t, h.ctx, other, getActivityConsumerTool, consumerArgs("worker"), false))
			if got.Status != "ready" || got.Revision != 1 {
				t.Fatal(got)
			}
			callTool(t, h.ctx, other, listActivityConsumersTool, consumerListArgs("", 1), false)
			// Rollout capacity also remains independent of activity observations.
			for range maxRolloutWatches {
				release, err := h.manager.admission.acquire(h.ctx, admissionRollout)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			// A distinct management connection has its own activity pool.
			separate := connectSubscriptionHarness(t, backend, opts, "2026-07-28", httpTransport)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			name, args := activityAdmissionArgs(false, 1)
			callTool(t, separate.ctx, separate.client, name, args, false)
			cancel()
			for range maxActivityWatches {
				select {
				case <-done:
				case <-h.ctx.Done():
					t.Fatal("watch cancellation blocked")
				}
			}
			deadline := time.Now().Add(time.Second)
			for len(h.manager.admission.activity) > 0 {
				if time.Now().After(deadline) {
					t.Fatal("activity slots leaked")
				}
				time.Sleep(time.Millisecond)
			}
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 0`)
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_admission_rejections_total{kind="short_requests"} 0`)
			if f.writes != 0 {
				t.Fatal("admission tests wrote native intent")
			}
		})
	}
}

func TestAdmissionActivityWatchesSurviveShortSaturation(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	opts.Telemetry = NewTelemetry()
	h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", true)
	callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	for range maxShortRequests {
		release, err := h.manager.admission.acquire(h.ctx, admissionShort)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	for _, consumer := range []bool{false, true} {
		name, args := activityAdmissionArgs(consumer, 1)
		callTool(t, h.ctx, h.client, name, args, false)
	}
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="short_requests"} 8`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 0`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_admission_rejections_total{kind="activity_watches"} 0`)
}

func TestAdmissionActivityWatchCleanup(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "drain"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			opts.Telemetry = NewTelemetry()
			opts.Lifecycle = NewLifecycle()
			var armed atomic.Bool
			entered, released := make(chan struct{}, maxActivityWatches), make(chan struct{}, maxActivityWatches)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if armed.Load() && strings.HasSuffix(r.URL.Path, "/activity") {
					entered <- struct{}{}
					<-r.Context().Done()
					released <- struct{}{}
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", true)
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			if mode == "deadline" {
				h.manager.opts.Timeout = 300 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			armed.Store(true)
			done := make(chan struct{}, maxActivityWatches)
			for i := range maxActivityWatches {
				name, args := activityAdmissionArgs(i%2 == 1, 5)
				go func() {
					defer func() { done <- struct{}{} }()
					_, _ = h.client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
				}()
			}
			for range maxActivityWatches {
				select {
				case <-entered:
				case <-h.ctx.Done():
					t.Fatal("watch did not reach native read")
				}
			}
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 2`)
			if mode == "cancel" {
				cancel()
			}
			if mode == "drain" {
				grace, stop := context.WithTimeout(h.ctx, time.Second)
				defer stop()
				if err := opts.Lifecycle.Shutdown(grace); err != nil {
					t.Fatal(err)
				}
			}
			for range maxActivityWatches {
				select {
				case <-done:
				case <-h.ctx.Done():
					t.Fatal("watch did not finish")
				}
				select {
				case <-released:
				case <-h.ctx.Done():
					t.Fatal("native request did not cancel")
				}
			}
			awaitAdmissionEmpty(t, h.manager.admission)
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 0`)
			if mode != "drain" {
				armed.Store(false)
				name, args := activityAdmissionArgs(true, 1)
				// A fast validation failure should reach the handler after cleanup,
				// rather than being rejected by leaked capacity.
				args["consumer"] = "../invalid"
				out := callTool(t, h.ctx, h.client, name, args, true)
				if string(out["code"]) != `"invalid_argument"` {
					t.Fatal(out)
				}
				awaitAdmissionEmpty(t, h.manager.admission)
			}
		})
	}
}

func TestAdmissionActivityWatchTimeoutAndErrorReuse(t *testing.T) {
	for _, consumer := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("consumer_%v_error_%v", consumer, failed), func(t *testing.T) {
				f, base, opts := newEnvironmentScheduleFixture(t)
				opts, _, _, _ = consumerOptions(t, opts)
				opts.Telemetry = NewTelemetry()
				var deny atomic.Bool
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if deny.Load() && strings.HasSuffix(r.URL.Path, "/activity") {
						http.Error(w, "unavailable", 500)
						return
					}
					base.Config.Handler.ServeHTTP(w, r)
				}))
				t.Cleanup(backend.Close)
				opts.APIBase = backend.URL
				h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
				callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
				deny.Store(failed)
				name, args := activityAdmissionArgs(consumer, 1)
				for range maxActivityWatches + 1 {
					out := callTool(t, h.ctx, h.client, name, args, failed)
					if !failed && string(out["watch_status"]) != `"timed_out"` {
						t.Fatal(out)
					}
					awaitAdmissionEmpty(t, h.manager.admission)
				}
				expectMetric(t, opts.Telemetry, `gregale_management_mcp_active{kind="activity_watches"} 0`)
				expectMetric(t, opts.Telemetry, `gregale_management_mcp_admission_rejections_total{kind="activity_watches"} 0`)
			})
		}
	}
}
