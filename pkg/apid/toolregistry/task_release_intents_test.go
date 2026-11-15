package toolregistry_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestMCPTaskReleaseIntentsMemory(t *testing.T) {
	s := state.NewMemStore()
	exerciseTaskIntents(t, s, func() state.Store { return s })
}

func TestMCPTaskReleaseIntentsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	exerciseTaskIntents(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) })
}

func exerciseTaskIntents(t *testing.T, s state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "task-review@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(label string, scopes ...string) state.APIKey {
		sum := sha256.Sum256([]byte(label))
		k, err := s.CreateAPIKey(ctx, account.ID, sum[:], label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	publisher := key("task publisher", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	owner := key("task owner", api.ScopeAdmin)
	other := key("other publisher", api.ScopeAgent, api.ScopeDeployWrite)
	agentAdmin := key("agent administrator", api.ScopeAdmin, api.ScopeAgent)
	in := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: api.MCPProtocolVersion,
		Endpoint: "/mcp", AccessMode: "open", ObserverMetricApp: "worker", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: "Review worker upgrade"}
	for _, role := range []string{"web", "worker", "observer"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: role, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: review})
	}
	call := func(method, id, suffix string, body any, k state.APIKey) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/task-tool-release-intents/"+id+suffix, bytes.NewReader(data))
		r.SetPathValue("id", id)
		r = r.WithContext(middleware.WithPrincipal(r.Context(), account, &k, nil))
		w := httptest.NewRecorder()
		toolregistry.NewPrivateExecutor(reopen(), toolregistry.PrivateExecutionOptions{}).TaskReleaseIntent()(w, r, account)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, status string) api.TaskToolReleaseIntentRecord {
		var out api.TaskToolReleaseIntentRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Status != status || out.ReadyToApply || out.BindingsVerified {
			t.Fatalf("review status %s: %d %s", status, w.Code, w.Body.String())
		}
		return out
	}
	body, _ := json.Marshal(in)
	var unsafe map[string]any
	_ = json.Unmarshal(body, &unsafe)
	unsafe["credential"] = "SECRET_SENTINEL"
	if w := call("PUT", fmt.Sprintf("%064x", 99), "", unsafe, owner); w.Code != 400 {
		t.Fatal("credential field accepted in an otherwise valid intent", w.Code)
	}
	for attempt := 0; attempt < 3; attempt++ {
		id := fmt.Sprintf("%064x", attempt+1)
		pending := decode(call("PUT", id, "", in, publisher), "pending")
		if retry := decode(call("PUT", id, "", in, publisher), "pending"); retry.IntentSHA256 != pending.IntentSHA256 || !retry.ExpiresAt.Equal(pending.ExpiresAt) {
			t.Fatal("prepare retry changed identity or deadline")
		}
		changed := in
		changed.Reason = "different review"
		if w := call("PUT", id, "", changed, publisher); w.Code != 409 {
			t.Fatal("intent overwritten", w.Code)
		}
		decision := api.TaskToolReleaseIntentDecision{IntentSHA256: pending.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint, Decision: "approve"}
		for _, forbidden := range []state.APIKey{publisher, agentAdmin} {
			if w := call("PUT", id, "/decision", decision, forbidden); w.Code == 200 {
				t.Fatal("agent approved its own review")
			}
		}
		if w := call("GET", id, "", nil, other); w.Code != 404 {
			t.Fatal("another publisher read intent", w.Code)
		}
		wrong := decision
		wrong.IntentSHA256 = strings.Repeat("f", 64)
		if w := call("PUT", id, "/decision", wrong, owner); w.Code != 409 {
			t.Fatal("wrong approval digest accepted", w.Code)
		}
		approved := decode(call("PUT", id, "/decision", decision, owner), "approved")
		decode(call("GET", id, "", nil, publisher), "approved") // New executor/store connection.
		if !approved.ExpiresAt.Equal(pending.ExpiresAt) {
			t.Fatal("approval extended expiry")
		}
		if attempt == 0 {
			decision.Decision = "revoke"
			decode(call("PUT", id, "/decision", decision, owner), "revoked")
			decision.Decision = "approve"
			if w := call("PUT", id, "/decision", decision, owner); w.Code != 409 {
				t.Fatal("revoked approval revived")
			}
		} else if attempt == 1 {
			decode(call("POST", id, "/check", api.TaskToolReleaseIntentCheck{ReleaseFingerprint: strings.Repeat("e", 64)}, publisher), "stale")
			decode(call("POST", id, "/check", api.TaskToolReleaseIntentCheck{ReleaseFingerprint: in.ReleaseFingerprint}, publisher), "stale")
		} else {
			_, err := s.CreateDeployment(ctx, state.Deployment{AppID: in.Apps[0].Review.AppID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding, SourceSHA256: strings.Repeat("d", 64), SourcePath: "/fixture/source.tar.gz", SourceBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			decode(call("POST", id, "/check", api.TaskToolReleaseIntentCheck{ReleaseFingerprint: in.ReleaseFingerprint}, publisher), "stale")
		}
	}
	for i := range in.Apps {
		current, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, in.Apps[i].Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		in.Apps[i].Review = current
	}
	for attempt := 0; attempt < 2; attempt++ {
		id := fmt.Sprintf("%064x", 10+attempt)
		pending := decode(call("PUT", id, "", in, publisher), "pending")
		d := api.TaskToolReleaseIntentDecision{Decision: "approve", IntentSHA256: pending.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint}
		decode(call("PUT", id, "/decision", d, owner), "approved")
		executionOwner := key(fmt.Sprintf("execution owner %d", attempt), api.ScopeAdmin)
		r := &api.TaskToolReleaseExecutionRequest{Action: "authorize", OperationID: fmt.Sprintf("%064x", 20+attempt), BindingsSHA256: strings.Repeat("b", 64)}
		d.Decision, d.Execution = "authorize", r
		if w := call("PUT", id, "/decision", d, publisher); w.Code == 200 {
			t.Fatal("requester authorized execution")
		}
		authorized := decode(call("PUT", id, "/decision", d, executionOwner), "approved")
		if authorized.Execution == nil || authorized.Execution.State != "authorized" || authorized.Execution.AuthorizerKeyID != executionOwner.ID {
			t.Fatal("missing separate execution authority", authorized)
		}
		r.Action, r.ClaimToken = "claim", strings.Repeat("c", 64)
		check := api.TaskToolReleaseIntentCheck{ReleaseFingerprint: in.ReleaseFingerprint, Execution: r}
		running := decode(call("POST", id, "/check", check, publisher), "approved")
		if running.Execution.State != "running" || running.Execution.ClaimSHA256 != toolpublication.Digest([]byte(r.ClaimToken)) {
			t.Fatal("claim not committed", running)
		}
		r.ClaimToken = strings.Repeat("d", 64)
		duplicate := decode(call("POST", id, "/check", check, publisher), "approved")
		if duplicate.Execution.ClaimSHA256 != running.Execution.ClaimSHA256 {
			t.Fatal("duplicate obtained a new lease")
		}
		r.Action = "guard"
		if w := call("POST", id, "/check", check, publisher); w.Code != 409 {
			t.Fatal("wrong claim guarded execution", w.Code)
		}
		r.ClaimToken = strings.Repeat("c", 64)
		if attempt == 0 {
			_, err := s.CreateDeployment(ctx, state.Deployment{AppID: in.Apps[0].Review.AppID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding, SourceSHA256: strings.Repeat("e", 64), SourcePath: "/fixture/next.tar.gz", SourceBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			stillRunning := decode(call("POST", id, "/check", check, publisher), "approved")
			if stillRunning.Execution.State != "running" {
				t.Fatal("own deployment invalidated stable configuration")
			}
			r.Action = "complete"
			completed := decode(call("POST", id, "/check", check, publisher), "approved")
			if completed.Execution.State != "completed" {
				t.Fatal("completion not durable")
			}
			for i := range in.Apps {
				current, e := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, in.Apps[i].Review.AppID, api.DefaultEnvScope)
				if e != nil {
					t.Fatal(e)
				}
				in.Apps[i].Review = current
			}
		} else {
			if _, err := s.MarkAPIKeyRevoked(ctx, account.ID, executionOwner.ID); err != nil {
				t.Fatal(err)
			}
			stopped := decode(call("POST", id, "/check", check, publisher), "stale")
			if stopped.Execution.State != "reconcile_required" {
				t.Fatal("revoked owner authority did not stop executor")
			}
		}
	}
	revokedID := fmt.Sprintf("%064x", 4)
	pending := decode(call("PUT", revokedID, "", in, publisher), "pending")
	decode(call("PUT", revokedID, "/decision", api.TaskToolReleaseIntentDecision{IntentSHA256: pending.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint, Decision: "approve"}, owner), "approved")
	cancelID := fmt.Sprintf("%064x", 5)
	cancelReview := decode(call("PUT", cancelID, "", in, publisher), "pending")
	decode(call("PUT", cancelID, "/decision", api.TaskToolReleaseIntentDecision{IntentSHA256: cancelReview.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint, Decision: "approve"}, owner), "approved")
	if _, err := s.MarkAPIKeyRevoked(ctx, account.ID, publisher.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", fmt.Sprintf("%064x", 1), "", nil, publisher); w.Code == 200 {
		t.Fatal("cached revoked principal retained access")
	}
	decode(call(http.MethodPost, revokedID, "/check", api.TaskToolReleaseIntentCheck{ReleaseFingerprint: in.ReleaseFingerprint}, owner), "stale")
	decode(call("PUT", cancelID, "/decision", api.TaskToolReleaseIntentDecision{IntentSHA256: cancelReview.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint, Decision: "revoke"}, owner), "revoked")
}
