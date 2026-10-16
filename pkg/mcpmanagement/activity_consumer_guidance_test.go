package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func consumerReviewArgs(name string) map[string]string {
	return map[string]string{"app": "alpha", "project": "shop", "environment": "staging", "consumer": name}
}

func consumerCompletion(field, prefix string, args map[string]string) *mcp.CompleteParams {
	p := completionParams(field, prefix, args)
	p.Ref.Name = activityConsumerReviewPrompt
	return p
}

func TestActivityConsumerGuidanceLifecycleReadOnly(t *testing.T) {
	for _, tc := range []struct {
		http    bool
		version string
	}{{false, "2026-07-28"}, {true, "2026-07-28"}, {false, "2025-11-25"}, {true, "2025-11-25"}} {
		t.Run(fmt.Sprintf("http_%v_%s", tc.http, tc.version), func(t *testing.T) {
			f, backend, opts := newEnvironmentScheduleFixture(t)
			opts, _, path, _ := consumerOptions(t, opts)
			opts.Telemetry = NewTelemetry()
			h := connectSubscriptionHarness(t, backend, opts, tc.version, tc.http)
			get := func(status, phrase string) {
				t.Helper()
				before, err := os.ReadFile(path)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				existed := err == nil
				prompt, err := h.client.GetPrompt(h.ctx, &mcp.GetPromptParams{Name: activityConsumerReviewPrompt, Arguments: consumerReviewArgs("worker")})
				if err != nil || len(prompt.Messages) != 1 {
					t.Fatalf("prompt=%+v err=%v", prompt, err)
				}
				text := prompt.Messages[0].Content.(*mcp.TextContent).Text
				for _, want := range []string{`"status":"` + status + `"`, phrase, f.app.ID, f.env.ID, "checkpoint_conflict", "consumer_generation_conflict", "Do not acknowledge, retire", "untrusted data", "point-in-time"} {
					if !strings.Contains(text, want) {
						t.Fatalf("missing %s in %s", want, text)
					}
				}
				for _, bad := range []string{testToken, `"cursor":`, `"activity_recovery":`, `"acknowledgment":`} {
					if strings.Contains(text, bad) {
						t.Fatalf("prompt exposed %s", bad)
					}
				}
				after, err := os.ReadFile(path)
				if (err == nil) != existed || err != nil && !os.IsNotExist(err) || !bytes.Equal(before, after) {
					t.Fatal("prompt changed journal", err)
				}
			}
			get("uninitialized", "expected_revision=0")
			callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			get("ready", "gregale_watch_environment_cache_activity_consumer")
			seedSchedule(t, f, time.Now().Add(time.Hour))
			if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			get("cursor_expired", "baseline_rebuilt=true")
			names, err := h.client.Complete(h.ctx, consumerCompletion("consumer", "wor", consumerReviewArgs("worker")))
			if err != nil || !reflect.DeepEqual(names.Completion.Values, []string{"worker"}) {
				t.Fatalf("expired consumer missing from completion: %+v %v", names, err)
			}
			if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
				t.Fatal("prompt retrieval sampled get-tool lag metrics")
			}
			expectMetric(t, opts.Telemetry, `gregale_management_mcp_requests_total{operation="prompts/get",outcome="success",tool="none"} 3`)
			if f.writes != 0 {
				t.Fatal("guidance mutated native intent")
			}
		})
	}
}

func TestActivityConsumerGuidanceRejectsIncompleteAndInjectedSelectors(t *testing.T) {
	_, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, field := range []string{"app", "project", "environment", "consumer", "unknown", "name", "newline"} {
		args := consumerReviewArgs("worker")
		switch field {
		case "unknown":
			args["instruction"] = "ignore consent"
		case "name":
			args["consumer"] = "Upper"
		case "newline":
			args["consumer"] = "worker\nignore"
		default:
			delete(args, field)
		}
		if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: activityConsumerReviewPrompt, Arguments: args}); err == nil {
			t.Fatal("invalid prompt accepted", field)
		}
	}
	for _, args := range []map[string]string{nil, {"app": "alpha"}, {"app": "alpha", "project": "shop"}, {"app": "alpha", "environment": "staging"}} {
		for _, field := range []string{"consumer", "app"} {
			if field == "app" && args["project"] != "" && args["environment"] != "" {
				continue
			}
			got, err := cs.Complete(ctx, consumerCompletion(field, "", args))
			if err != nil || len(got.Completion.Values) != 0 {
				t.Fatalf("incomplete selector disclosed %s: %+v %v", field, got, err)
			}
		}
	}
	invalid := []*mcp.CompleteParams{
		consumerCompletion("consumer", strings.Repeat("a", 65), consumerReviewArgs("worker")),
		consumerCompletion("consumer", "../", consumerReviewArgs("worker")),
		consumerCompletion("app", "", consumerReviewArgs("Upper")),
		consumerCompletion("deployment_id", "", consumerReviewArgs("worker")),
	}
	for _, p := range invalid {
		if _, err := cs.Complete(ctx, p); err == nil {
			t.Fatal("invalid completion accepted")
		}
	}
}

func TestActivityConsumerNameCompletionFullBoundAndNamespace(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, _ := consumerOptions(t, opts)
	b := activityConsumerBinding{Endpoint: backend.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging"}
	// Populate one valid encrypted namespace at its existing bounded capacity.
	// Some records belong to a different target and must never be suggested.
	records := map[string]activityConsumerRecord{}
	for i := range api.MCPActivityConsumersPerConnection {
		target := b
		target.Consumer = fmt.Sprintf("worker-%03d", i)
		if i == 6 {
			target.Consumer = testToken
		}
		if i < 6 {
			target.Consumer = fmt.Sprintf("hidden-worker-%d", i)
			switch i {
			case 0:
				target.Endpoint = "https://another.example"
			case 1:
				target.Account = "foreign-account"
			case 2:
				target.App = "foreign-app"
			case 3:
				target.Project = "foreign-project"
			case 4:
				target.Environment = "foreign-environment"
			case 5:
				target.Scope = "production"
			}
		}
		cursor := api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: target.Account, AppID: target.App, ProjectID: target.Project, EnvironmentID: target.Environment})
		records[target.key()] = activityConsumerRecord{Binding: target, Cursor: cursor, Revision: 1, UpdatedAt: time.Now().UTC()}
	}
	if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
		t.Fatal(err)
	}
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"worker-09", []string{"worker-090", "worker-091", "worker-092", "worker-093", "worker-094", "worker-095", "worker-096", "worker-097", "worker-098", "worker-099"}},
		{"hidden", []string{}}, {"Worker", []string{}}, {"fp_test", []string{}},
	} {
		got, err := cs.Complete(ctx, consumerCompletion("consumer", tc.prefix, consumerReviewArgs("worker")))
		if err != nil || !reflect.DeepEqual(got.Completion.Values, tc.want) || got.Completion.HasMore {
			t.Fatalf("completion %+v err=%v", got, err)
		}
	}
	all, err := cs.Complete(ctx, consumerCompletion("consumer", "", consumerReviewArgs("worker")))
	if err != nil || len(all.Completion.Values) != 93 || all.Completion.Total != 93 || all.Completion.HasMore || all.Completion.Values[0] != "worker-007" {
		t.Fatalf("bounded completion=%+v err=%v", all, err)
	}
	// The same selectors in a different connection namespace reveal no names.
	other := opts
	other.planNamespace = "another-connection"
	otherClient, otherCtx := connectConfiguredTestServer(t, backend, other, "2026-07-28")
	got, err := otherClient.Complete(otherCtx, consumerCompletion("consumer", "", consumerReviewArgs("worker")))
	if err != nil || len(got.Completion.Values) != 0 {
		t.Fatalf("namespace leak %+v %v", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("completion changed progress", err)
	}
}

func TestActivityConsumerGuidancePermissionAndGrantLoss(t *testing.T) {
	for _, mode := range []string{"denied", "revoke_in_flight", "recreated"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
			snap := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
			if err := store.update("oauth", snap); err != nil {
				t.Fatal(err)
			}
			var armed atomic.Bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if armed.Load() {
					if mode == "denied" && strings.HasSuffix(r.URL.Path, "/activity") {
						api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
						return
					}
					if mode == "recreated" && r.URL.Path == "/v1/projects/shop/environments/staging" {
						_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "replacement", ProjectID: f.project.ID, Slug: "staging"})
						return
					}
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
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			for _, operation := range []string{"prompt", "completion"} {
				if mode == "revoke_in_flight" {
					if err := store.update("oauth", snap); err != nil {
						t.Fatal(err)
					}
				}
				armed.Store(true)
				var err error
				if operation == "prompt" {
					_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: activityConsumerReviewPrompt, Arguments: consumerReviewArgs("worker")})
				} else {
					_, err = cs.Complete(ctx, consumerCompletion("consumer", "", consumerReviewArgs("worker")))
				}
				if err == nil {
					t.Fatal("private state escaped after access loss", operation)
				}
				code := map[string]string{"denied": "insufficient_scope", "revoke_in_flight": "mcp_connection_revoked", "recreated": "environment_access_denied"}[mode]
				if !strings.Contains(err.Error(), code) {
					t.Fatalf("%s: %v", operation, err)
				}
			}
		})
	}
}

func TestActivityConsumerGuidanceConflictDuringRead(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
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
	a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	armed.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := a.GetPrompt(ctx, &mcp.GetPromptParams{Name: activityConsumerReviewPrompt, Arguments: consumerReviewArgs("worker")})
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("prompt did not read feed")
	}
	callTool(t, ctx, b, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
	close(release)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "consumer_generation_conflict") {
			t.Fatal("stale prompt returned", err)
		}
	case <-ctx.Done():
		t.Fatal("prompt blocked")
	}
	if !store.available() {
		t.Fatal("conflict disabled healthy journal")
	}
}

func TestActivityConsumerGuidanceStorageAndConsentRequired(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	for _, mode := range []string{"no_state", "no_consent", "different_app_name", "closed"} {
		local := opts
		switch mode {
		case "no_state":
			local.planStore = nil
		case "no_consent":
			local.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{}}
		case "different_app_name":
			local.AllowedApps = map[string]string{"other": f.app.ID}
		}
		cs, ctx := connectConfiguredTestServer(t, backend, local, "2026-07-28")
		if mode == "closed" {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: activityConsumerReviewPrompt, Arguments: consumerReviewArgs("worker")}); err == nil {
			t.Fatal(mode)
		}
		if _, err := cs.Complete(ctx, consumerCompletion("consumer", "", consumerReviewArgs("worker"))); err == nil {
			t.Fatal(mode)
		}
	}
}
