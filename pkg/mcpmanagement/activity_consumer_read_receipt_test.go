package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestActivityConsumerReadGuardPreservesMutationReceipts(t *testing.T) {
	for _, retire := range []bool{false, true} {
		name := "ack"
		if retire {
			name = "retire"
		}
		t.Run(name, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			opts.Telemetry = NewTelemetry()
			var ready atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/projects/shop" && ready.CompareAndSwap(true, false) {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			opts.APIBase = backend.URL
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", true, func(s *mcp.Server, m *management) {
				if retire {
					registerToolAnnotations(s, m, retireActivityConsumerTool, "Retirement receipt test", false, false, true, signalConsumerRead(&ready, m.retireActivityConsumer))
				} else {
					registerToolEffect(s, m, ackActivityConsumerTool, "ACK receipt test", false, false, signalConsumerRead(&ready, m.ackActivityConsumer))
				}
			})
			b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			tool, args := ackActivityConsumerTool, consumerAck("alpha", activityCursor(t, f, 0), 0, false)
			if retire {
				callTool(t, h.ctx, b, ackActivityConsumerTool, args, false)
				tool, args = retireActivityConsumerTool, consumerRetirement("alpha", 1, "")
			}
			type answer struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				r, e := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("mutation did not commit before its final authorization read")
			}
			if retire {
				var epoch activityConsumerEpoch
				if err := store.read("activity-consumer-generations/"+opts.planNamespace, &epoch); err != nil || epoch.Generation == "" {
					t.Fatal(epoch, err)
				}
				callTool(t, h.ctx, b, ackActivityConsumerTool, ackInGeneration("alpha", activityCursor(t, f, 0), 0, epoch.Generation), false)
			} else {
				callTool(t, h.ctx, b, retireActivityConsumerTool, consumerRetirement("alpha", 1, ""), false)
			}
			close(release)
			select {
			case out := <-done:
				if out.err != nil || out.result == nil || out.result.IsError {
					t.Fatal("read guard discarded a committed mutation receipt", out.result, out.err)
				}
				data, _ := json.Marshal(out.result.StructuredContent)
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(data, &fields)
				if retire && string(fields["status"]) != `"retired"` || !retire && string(fields["revision"]) != "1" {
					t.Fatal(fields)
				}
			case <-h.ctx.Done():
				t.Fatal("mutation receipt missing")
			}
			if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") || f.writes != 0 {
				t.Fatal("mutation receipt sampled read metrics or wrote native intent")
			}
		})
	}
}

func TestActivityConsumerGetMetricsAfterSerialization(t *testing.T) {
	_, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	opts.Telemetry = NewTelemetry()
	h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
	value, err := h.manager.getActivityConsumer(h.ctx, activityConsumerArgs{App: "alpha", Project: "shop", Environment: "staging", Consumer: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []any{make(chan string), strings.Repeat("x", maxResultBytes+1)} {
		broken := value.(consumerToolReadResult)
		broken.value = data
		if _, err := h.manager.finalToolOutput(h.ctx, broken); err == nil {
			t.Fatal("invalid output was accepted")
		}
		if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
			t.Fatal("unserializable or oversized read sampled lag")
		}
	}
	if _, err := h.manager.finalToolOutput(h.ctx, value); err != nil {
		t.Fatal(err)
	}
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_reads_total{status="uninitialized"} 1`)
}
