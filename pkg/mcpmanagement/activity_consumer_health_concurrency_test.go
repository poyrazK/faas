package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestActivityConsumerHealthInFlightGuards(t *testing.T) {
	type scenario struct{ phase, change string }
	var cases []scenario
	for _, change := range []string{"ack", "create", "retire", "revoke", "storage", "deny", "identity", "cancel", "drain", "retention"} {
		cases = append(cases, scenario{"feed", change})
	}
	for _, change := range []string{"ack", "retire", "revoke"} {
		cases = append(cases, scenario{"final_authorization", change})
	}
	for _, tc := range cases {
		t.Run(tc.phase+"/"+tc.change, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			if tc.change == "revoke" {
				opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
				snapshot := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
				if err := store.update("oauth", snapshot); err != nil {
					t.Fatal(err)
				}
			}
			var armed, feedRead, deny, recreated atomic.Bool
			var afterFeed atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if recreated.Load() && r.URL.Path == "/v1/projects/shop/environments/staging" {
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "replacement", ProjectID: f.project.ID, Slug: "staging"})
					return
				}
				feed := strings.HasSuffix(r.URL.Path, "/activity")
				if tc.phase == "final_authorization" && armed.Load() && feed {
					feedRead.Store(true)
				}
				// One project read completes the feed's identity validation. The
				// next is the generic tool wrapper's post-handler authorization.
				final := tc.phase == "final_authorization" && feedRead.Load() && r.URL.Path == "/v1/projects/shop" && afterFeed.Add(1) == 2
				if (tc.phase == "feed" && feed || final) && armed.CompareAndSwap(true, false) {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if feed && deny.Load() {
					api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
					return
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
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
			b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			for _, name := range []string{"alpha", "bravo", "charlie"} {
				callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
			}
			seedSchedule(t, f, time.Now().Add(time.Hour))
			armed.Store(true)
			callCtx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			type answer struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				r, e := h.client.CallTool(callCtx, &mcp.CallToolParams{Name: activityConsumerHealthTool, Arguments: consumerListArgs("", 1)})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("health did not reach the guarded read")
			}
			code := "checkpoint_conflict"
			switch tc.change {
			case "ack":
				callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck("charlie", activityCursor(t, f, 1), 1, false), false)
			case "create":
				callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck("delta", activityCursor(t, f, 0), 0, false), false)
			case "retire":
				callTool(t, h.ctx, b, retireActivityConsumerTool, consumerRetirement("charlie", 1, ""), false)
				code = "consumer_generation_conflict"
			case "revoke":
				if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
					t.Fatal(err)
				}
				code = "mcp_connection_revoked"
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				code = "durable_state_unavailable"
			case "deny":
				deny.Store(true)
				code = "insufficient_scope"
			case "identity":
				recreated.Store(true)
				code = "environment_access_denied"
			case "cancel":
				cancel()
				code = "cancelled"
			case "drain":
				h.manager.lifecycle.BeginDrain()
				code = "cancelled"
			case "retention":
				seedSchedule(t, f, time.Now().Add(2*time.Hour))
				if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			select {
			case out := <-done:
				if tc.change == "cancel" && out.err != nil {
					return
				}
				if out.err != nil {
					t.Fatal(out.err)
				}
				if out.result == nil {
					t.Fatal("health result missing")
				}
				data, _ := json.Marshal(out.result.StructuredContent)
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if tc.change == "retention" {
					if out.result.IsError {
						t.Fatal(fields)
					}
					health := decodeConsumerHealth(t, fields)
					if health.Summary != (activityConsumerHealthSummary{Total: 3, CursorExpired: 3, NeedsAttention: 3}) || *health.Entries[0].Lag.ExpiredUnacknowledgedEvents != 2 || *health.Entries[0].Lag.RetainedPendingEvents != 0 {
						t.Fatal("overtaken head did not use validated fresh bounds", health)
					}
				} else {
					if !out.result.IsError {
						t.Fatal("health released stale state", fields)
					}
					noConsumerHealth(t, fields, code)
				}
			case <-h.ctx.Done():
				t.Fatal("health read did not finish")
			}
		})
	}
}

func TestActivityConsumerHealthOldDiscoveryCheckpoint(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activity/checkpoint") && armed.CompareAndSwap(true, false) {
			captured := httptest.NewRecorder()
			base.Config.Handler.ServeHTTP(captured, r)
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			for key, values := range captured.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(captured.Code)
			_, _ = w.Write(captured.Body.Bytes())
			return
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
	h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
	b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	armed.Store(true)
	type answer struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		r, e := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: activityConsumerHealthTool, Arguments: consumerListArgs("", 1)})
		done <- answer{r, e}
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("discovery did not capture the old checkpoint")
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
	close(release)
	select {
	case out := <-done:
		if out.err != nil || out.result == nil || out.result.IsError {
			t.Fatal("health reused a checkpoint predating saved progress", out.result, out.err)
		}
		data, _ := json.Marshal(out.result.StructuredContent)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		health := decodeConsumerHealth(t, fields)
		if health.Summary != (activityConsumerHealthSummary{Total: 1, Ready: 1}) || health.Entries[0].Revision != 2 || *health.Entries[0].Lag.RetainedPendingEvents != 0 {
			t.Fatal(health)
		}
	case <-h.ctx.Done():
		t.Fatal("health did not finish")
	}
}

func TestActivityConsumerHealthInvalidStateAndConsent(t *testing.T) {
	for _, mode := range []string{"no_state", "no_namespace", "no_consent", "foreign_app", "closed", "future_cursor", "bad_name", "bad_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			backend := base
			code := "durable_state_required"
			switch mode {
			case "no_state":
				opts.planStore = nil
			case "no_namespace":
				opts.planNamespace = ""
			case "no_consent":
				opts.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{}}
				code = "environment_access_denied"
			case "foreign_app":
				opts.AllowedApps = map[string]string{"other": f.app.ID}
				code = "app_access_denied"
			case "closed":
				code = "durable_state_unavailable"
			case "future_cursor", "bad_name":
				code = "invalid_response"
				b := activityConsumerBinding{Endpoint: base.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging", Consumer: "zebra"}
				cursor := activityCursor(t, f, 1)
				if mode == "bad_name" {
					b.Consumer, cursor, code = "Upper", activityCursor(t, f, 0), "invalid_state"
				}
				records := map[string]activityConsumerRecord{b.key(): {Binding: b, Cursor: cursor, Revision: 1, UpdatedAt: time.Now().UTC()}}
				b.Consumer = "alpha"
				records[b.key()] = activityConsumerRecord{Binding: b, Cursor: activityCursor(t, f, 0), Revision: 1, UpdatedAt: time.Now().UTC()}
				if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
					t.Fatal(err)
				}
			case "bad_checkpoint":
				code = "invalid_response"
				backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/activity/checkpoint") {
						point, err := f.store.GetEnvironmentCacheScheduleActivityCheckpoint(r.Context(), f.account.ID, f.app.ID, "staging")
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						point.Scope = "production"
						_ = json.NewEncoder(w).Encode(point)
						return
					}
					base.Config.Handler.ServeHTTP(w, r)
				}))
				t.Cleanup(backend.Close)
				opts.APIBase = backend.URL
			}
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			if mode == "closed" {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs("", 1), true), code)
		})
	}
}
