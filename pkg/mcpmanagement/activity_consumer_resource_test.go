package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func consumerResourceURI(name string) string {
	return "gregale://projects/shop/environments/staging/apps/alpha/activity-consumers/" + name
}

func readConsumerResource(t *testing.T, h *subscriptionHarness, name string) activityConsumerResult {
	t.Helper()
	result, err := h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: consumerResourceURI(name)})
	if err != nil || len(result.Contents) != 1 {
		t.Fatalf("resource=%+v err=%v", result, err)
	}
	text := result.Contents[0].Text
	for _, bad := range []string{testToken, `"cursor":`, `"activity_cursor":`, `"activity_recovery":`, `"acknowledgment":`, `"binding":`, `"hint":`} {
		if strings.Contains(text, bad) {
			t.Fatalf("resource exposed %s", bad)
		}
	}
	var state activityConsumerResult
	if result.Contents[0].URI != consumerResourceURI(name) || result.Contents[0].MIMEType != "application/json" || json.Unmarshal([]byte(text), &state) != nil || state.Lag == nil {
		t.Fatal("invalid consumer resource", text)
	}
	return state
}

func awaitConsumerResourceUpdate(t *testing.T, h *subscriptionHarness, name string) {
	t.Helper()
	select {
	case p := <-h.updates:
		if p.URI != consumerResourceURI(name) || p.Meta[mcp.MetaKeySubscriptionID] == nil {
			t.Fatalf("unexpected resource invalidation: %+v", p)
		}
	case <-h.ctx.Done():
		t.Fatal("consumer change was not delivered")
	}
}

func quietConsumerResource(t *testing.T, h *subscriptionHarness) {
	t.Helper()
	select {
	case p := <-h.updates:
		t.Fatalf("unexpected consumer invalidation: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestActivityConsumerResourceReadsAndCompletion(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, remote := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_http_%v", version, remote), func(t *testing.T) {
				f, backend, opts := newEnvironmentScheduleFixture(t)
				opts, _, path, _ := consumerOptions(t, opts)
				opts.Telemetry = NewTelemetry()
				h := connectSubscriptionHarness(t, backend, opts, version, remote)
				templates, err := h.client.ListResourceTemplates(h.ctx, nil)
				if err != nil || len(templates.ResourceTemplates) != 8 {
					t.Fatal(templates, err)
				}
				found := false
				for _, template := range templates.ResourceTemplates {
					found = found || template.URITemplate == activityConsumerResourceTemplate
				}
				if !found || activeResourceGroups(h.manager) != 0 {
					t.Fatal("resource discovery missing or started polling")
				}
				if state := readConsumerResource(t, h, "worker"); state.Status != "uninitialized" || state.Revision != 0 || state.AppID != f.app.ID || state.EnvironmentID != f.env.ID || state.Lag.RetainedPendingEvents != nil {
					t.Fatal(state)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("resource created progress", err)
				}
				callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
				seedSchedule(t, f, time.Now().Add(time.Hour))
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if state := readConsumerResource(t, h, "worker"); state.Status != "ready" || state.Revision != 1 || *state.Lag.RetainedPendingEvents != 1 || *state.Lag.ExpiredUnacknowledgedEvents != 0 {
					t.Fatal(state)
				}
				if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				if state := readConsumerResource(t, h, "worker"); state.Status != "cursor_expired" || state.Revision != 1 || *state.Lag.RetainedPendingEvents != 0 || *state.Lag.ExpiredUnacknowledgedEvents != 1 {
					t.Fatal(state)
				}
				params := consumerCompletion("consumer", "wor", consumerReviewArgs("worker"))
				params.Ref = &mcp.CompleteReference{Type: "ref/resource", URI: activityConsumerResourceTemplate}
				got, err := h.client.Complete(h.ctx, params)
				if err != nil || !reflect.DeepEqual(got.Completion.Values, []string{"worker"}) {
					t.Fatal(got, err)
				}
				params.Context = nil
				got, err = h.client.Complete(h.ctx, params)
				if err != nil || len(got.Completion.Values) != 0 {
					t.Fatal("incomplete resource selector leaked names", got, err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) || f.writes != 0 {
					t.Fatal("resource/completion mutated state", err)
				}
				if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
					t.Fatal("resource sampled get-tool lag metrics")
				}
				if version == "2025-11-25" && h.client.InitializeResult().Capabilities.Resources.Subscribe {
					t.Fatal("legacy subscription support advertised")
				}
			})
		}
	}
}

func TestActivityConsumerResourceNotifications(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("http_%v", remote), func(t *testing.T) {
			f, backend, opts := newEnvironmentScheduleFixture(t)
			opts, _, path, _ := consumerOptions(t, opts)
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", remote)
			// This test drives a multi-step lifecycle; its request context bounds
			// cleanup, while the listen must remain live through all transitions.
			h.manager.subscriptions.ttl = resourceSubscriptionTTL
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			h.subscribe(t, consumerResourceURI("worker"))
			before, _ := os.ReadFile(path)
			quietConsumerResource(t, h)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			awaitConsumerResourceUpdate(t, h, "worker")
			if state := readConsumerResource(t, h, "worker"); state.Revision != 1 || *state.Lag.RetainedPendingEvents != 1 {
				t.Fatal(state)
			}
			quietConsumerResource(t, h)
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("subscription advanced progress")
			}
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
			awaitConsumerResourceUpdate(t, h, "worker")
			if state := readConsumerResource(t, h, "worker"); state.Revision != 2 || *state.Lag.RetainedPendingEvents != 0 {
				t.Fatal(state)
			}
			seedSchedule(t, f, time.Now().Add(2*time.Hour))
			awaitConsumerResourceUpdate(t, h, "worker")
			if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			awaitConsumerResourceUpdate(t, h, "worker")
			if state := readConsumerResource(t, h, "worker"); state.Status != "cursor_expired" || *state.Lag.ExpiredUnacknowledgedEvents != 1 {
				t.Fatal(state)
			}
			baseline := decodeConsumerRecovery(t, callTool(t, h.ctx, h.client, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 50), false))
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, baseline.Acknowledgment, false)
			awaitConsumerResourceUpdate(t, h, "worker")
			retired := decodeConsumer(t, callTool(t, h.ctx, h.client, retireActivityConsumerTool, consumerRetirement("worker", 3, ""), false))
			awaitConsumerResourceUpdate(t, h, "worker")
			if state := readConsumerResource(t, h, "worker"); state.Status != "uninitialized" || state.Generation != retired.Generation || state.Generation == "" {
				t.Fatal(state)
			}
			args := consumerAck("worker", activityCursor(t, f, 2), 0, false)
			args["expected_generation"] = retired.Generation
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, args, false)
			awaitConsumerResourceUpdate(t, h, "worker")
			quietConsumerResource(t, h)
			if err := h.client.Unsubscribe(h.ctx, &mcp.UnsubscribeParams{URI: consumerResourceURI("worker")}); err != nil {
				t.Fatal(err)
			}
			awaitResourceGroups(t, h, 0)
			_ = h.client.Close()
			reconnected := connectSubscriptionHarness(t, backend, opts, "2026-07-28", remote)
			reconnected.manager.subscriptions.ttl = resourceSubscriptionTTL
			if state := readConsumerResource(t, reconnected, "worker"); state.Revision != 1 || state.Generation != retired.Generation {
				t.Fatal("reconnect lost consumer state", state)
			}
			seedSchedule(t, f, time.Now().Add(3*time.Hour))
			quietConsumerResource(t, reconnected)
			if activeResourceGroups(reconnected.manager) != 0 {
				t.Fatal("reconnect implicitly resumed polling")
			}
			reconnected.subscribe(t, consumerResourceURI("worker"))
			seedSchedule(t, f, time.Now().Add(4*time.Hour))
			awaitConsumerResourceUpdate(t, reconnected, "worker")
			if f.writes != 0 {
				t.Fatal("consumer observations wrote native intent")
			}
		})
	}
}

func TestActivityConsumerResourceSemanticDigest(t *testing.T) {
	zero, one := int64(0), int64(1)
	age := float64(1)
	state := activityConsumerResult{Consumer: "worker", Status: "ready", Revision: 1, Lag: &activityConsumerLag{ObservedAt: time.Now(), RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds, SecondsSinceAck: &age, RetainedPendingEvents: &zero, ExpiredUnacknowledgedEvents: &zero}}
	digest := func(value activityConsumerResult) [32]byte {
		t.Helper()
		data, _ := json.Marshal(value)
		h, err := contextResourceDigest(data, activityConsumerResourceView)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	original := digest(state)
	state.Lag.ObservedAt = time.Now().Add(time.Hour)
	age = 900
	state.Lag.AckTimeInFuture = true
	if digest(state) != original {
		t.Fatal("clock-only change emitted invalidation")
	}
	for _, field := range []string{"status", "revision", "generation", "backlog", "gap", "retention"} {
		changed, lag := state, *state.Lag
		changed.Lag = &lag
		switch field {
		case "status":
			changed.Status = "cursor_expired"
		case "revision":
			changed.Revision++
		case "generation":
			changed.Generation = "new"
		case "backlog":
			lag.RetainedPendingEvents = &one
		case "gap":
			lag.ExpiredUnacknowledgedEvents = &one
		case "retention":
			lag.RetentionSeconds++
		}
		if digest(changed) == original {
			t.Fatal("hidden semantic change", field)
		}
	}
}

func TestActivityConsumerResourceRejectsInvalidSelectorsAndState(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	for _, uri := range []string{consumerResourceURI(""), consumerResourceURI("Upper"), consumerResourceURI(strings.Repeat("x", 65)), consumerResourceURI("worker") + "/", consumerResourceURI("worker") + "?token=secret", consumerResourceURI("worker") + "#fragment", consumerResourceURI("a%2Fb"), strings.Replace(consumerResourceURI("worker"), "projects/shop/environments/staging", "default", 1)} {
		if _, _, err := parseContextURI(uri); err == nil {
			t.Fatal("invalid consumer resource accepted", uri)
		}
	}
	for _, mode := range []string{"no_state", "no_consent", "foreign_app", "closed"} {
		local := opts
		switch mode {
		case "no_state":
			local.planStore = nil
		case "no_consent":
			local.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{}}
		case "foreign_app":
			local.AllowedApps = map[string]string{"other": f.app.ID}
		}
		cs, ctx := connectConfiguredTestServer(t, backend, local, "2026-07-28")
		if mode == "closed" {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: consumerResourceURI("worker")}); err == nil {
			t.Fatal("private resource escaped", mode)
		}
	}
}

func TestActivityConsumerResourcePermissionLoss(t *testing.T) {
	for _, mode := range []string{"deny", "revoke", "revoke_in_flight", "identity", "storage", "expiry", "disconnect", "drain"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
			grant := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", AccountID: f.account.ID, Active: true, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
			if err := store.update("oauth", grant); err != nil {
				t.Fatal(err)
			}
			var armed atomic.Bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if armed.Load() && mode == "deny" && strings.HasSuffix(r.URL.Path, "/activity") {
					api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
					return
				}
				if armed.Load() && mode == "identity" && r.URL.Path == "/v1/projects/shop/environments/staging" {
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "replacement", ProjectID: f.project.ID, Slug: "staging"})
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
				if mode == "revoke_in_flight" && strings.HasSuffix(r.URL.Path, "/activity") && armed.CompareAndSwap(true, false) {
					if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
						t.Error(err)
					}
				}
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", true)
			if mode == "revoke_in_flight" {
				h.manager.subscriptions.poll = time.Hour
			}
			if mode == "expiry" {
				// Leave time for validated baseline reads and acknowledgment under
				// the race detector before testing automatic listen expiry.
				h.manager.subscriptions.ttl = 500 * time.Millisecond
			}
			h.subscribe(t, consumerResourceURI("worker"))
			switch mode {
			case "deny", "identity", "revoke_in_flight":
				armed.Store(true)
			case "revoke":
				if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
					t.Fatal(err)
				}
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				_ = h.client.Close()
			case "drain":
				h.manager.lifecycle.BeginDrain()
			}
			if mode == "revoke_in_flight" {
				if err := h.manager.resourceServer.ResourceUpdated(h.ctx, &mcp.ResourceUpdatedNotificationParams{URI: consumerResourceURI("worker")}); err != nil {
					t.Fatal(err)
				}
			}
			awaitResourceGroups(t, h, 0)
			quietConsumerResource(t, h)
		})
	}
}

func TestActivityConsumerResourceConnectionIsolation(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	a := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
	other := opts
	other.planNamespace = "another-connection"
	b := connectSubscriptionHarness(t, backend, other, "2026-07-28", false)
	callTool(t, a.ctx, a.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	if state := readConsumerResource(t, b, "worker"); state.Revision != 0 || state.Status != "uninitialized" {
		t.Fatal("consumer escaped its connection", state)
	}
	a.subscribe(t, consumerResourceURI("worker"))
	b.subscribe(t, consumerResourceURI("worker"))
	seedSchedule(t, f, time.Now().Add(time.Hour))
	awaitConsumerResourceUpdate(t, a, "worker")
	quietConsumerResource(t, b)
	callTool(t, b.ctx, b.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 0, false), false)
	awaitConsumerResourceUpdate(t, b, "worker")
	quietConsumerResource(t, a)
	callTool(t, a.ctx, a.client, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
	awaitConsumerResourceUpdate(t, a, "worker")
	quietConsumerResource(t, b)
	if state := readConsumerResource(t, b, "worker"); state.Generation != "" || state.Revision != 1 || *state.Lag.RetainedPendingEvents != 0 {
		t.Fatal("foreign retirement changed consumer", state)
	}
}

func TestActivityConsumerResourceBusyPollRetries(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
	callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	h.subscribe(t, consumerResourceURI("worker"))
	var release []func()
	for range api.MCPShortRequestsPerConnection {
		stop, err := h.manager.admission.acquire(h.ctx, admissionShort)
		if err != nil {
			t.Fatal(err)
		}
		stop = sync.OnceFunc(stop)
		t.Cleanup(stop)
		release = append(release, stop)
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	quietConsumerResource(t, h)
	if activeResourceGroups(h.manager) != 1 {
		t.Fatal("busy poll closed the subscription")
	}
	for _, stop := range release {
		stop()
	}
	awaitConsumerResourceUpdate(t, h, "worker")
}

func TestActivityConsumerResourceConcurrentChangeRetries(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprintf("retire_%v", retire), func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			var armed atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/activity") && armed.CompareAndSwap(true, false) {
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
			h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			h.subscribe(t, consumerResourceURI("worker"))
			seedSchedule(t, f, time.Now().Add(time.Hour))
			armed.Store(true)
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("subscription did not read feed")
			}
			if retire {
				callTool(t, h.ctx, h.client, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
			} else {
				callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
			}
			close(release)
			awaitConsumerResourceUpdate(t, h, "worker")
			state := readConsumerResource(t, h, "worker")
			if retire && (state.Status != "uninitialized" || state.Generation == "") || !retire && (state.Status != "ready" || state.Revision != 2) {
				t.Fatal(state)
			}
			if activeResourceGroups(h.manager) != 1 {
				t.Fatal("consumer conflict cancelled a healthy subscription")
			}
		})
	}
}

func TestActivityConsumerResourceFinalConsentReadGuard(t *testing.T) {
	for _, operation := range []string{"read", "snapshot", "send"} {
		for _, revoke := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_revoke_%v", operation, revoke), func(t *testing.T) {
				f, base, opts := newEnvironmentScheduleFixture(t)
				opts, store, _, _ := consumerOptions(t, opts)
				opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
				grant := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
				if err := store.update("oauth", grant); err != nil {
					t.Fatal(err)
				}
				var armed, feedRead atomic.Bool
				var afterFeed atomic.Int32
				entered, release := make(chan struct{}), make(chan struct{})
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if armed.Load() && strings.HasSuffix(r.URL.Path, "/activity") {
						feedRead.Store(true)
					}
					// The first project read after the feed belongs to its own final
					// identity check. The second occurs AFTER consumer inspection,
					// during the resource wrapper's final consent/identity reads.
					if armed.Load() && feedRead.Load() && r.URL.Path == "/v1/projects/shop" && afterFeed.Add(1) == 2 {
						armed.Store(false)
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
				h := connectSubscriptionHarness(t, backend, opts, "2026-07-28", false)
				h.manager.subscriptions.poll = time.Hour
				callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
				if operation == "send" {
					h.subscribe(t, consumerResourceURI("worker"))
				}
				seedSchedule(t, f, time.Now().Add(time.Hour))
				armed.Store(true)
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "read":
						_, err = h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: consumerResourceURI("worker")})
					case "snapshot":
						args, view, _ := parseContextURI(consumerResourceURI("worker"))
						_, _, err = h.manager.subscriptionSnapshot(h.ctx, args, view, nil)
					case "send":
						err = h.manager.resourceServer.ResourceUpdated(h.ctx, &mcp.ResourceUpdatedNotificationParams{URI: consumerResourceURI("worker")})
					}
					done <- err
				}()
				select {
				case <-entered:
				case <-h.ctx.Done():
					t.Fatal("final consent read did not start")
				}
				code := "checkpoint_conflict"
				if revoke {
					if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
						t.Fatal(err)
					}
					code = "mcp_connection_revoked"
				} else {
					callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
				}
				close(release)
				select {
				case err := <-done:
					if operation == "read" && (err == nil || !strings.Contains(err.Error(), code)) {
						t.Fatal("resource escaped final guard", err)
					}
					if operation == "snapshot" {
						var failure *inputError
						if !errors.As(err, &failure) || failure.code != code || !revoke && !retryResourceSnapshot(err) {
							t.Fatal("snapshot escaped final guard", err)
						}
					}
					if operation == "send" && err != nil {
						t.Fatal(err)
					}
				case <-h.ctx.Done():
					t.Fatal("resource did not finish")
				}
				quietConsumerResource(t, h)
				if operation == "send" {
					if revoke {
						awaitResourceGroups(t, h, 0)
					} else {
						if activeResourceGroups(h.manager) != 1 {
							t.Fatal("conflict closed a healthy recipient")
						}
						_ = h.manager.resourceServer.ResourceUpdated(h.ctx, &mcp.ResourceUpdatedNotificationParams{URI: consumerResourceURI("worker")})
						awaitConsumerResourceUpdate(t, h, "worker")
					}
				}
			})
		}
	}
}
