// adr: 20261017222622
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const regressionResourceURI = "gregale://apps/alpha/regressions"

type regressionResourceFixture struct {
	mu                             sync.Mutex
	appID, deploymentAppID, scope  string
	afterAppID, afterScope         string
	afterDeploymentAppID           string
	status                         int
	native                         api.DebugRegressionsResponse
	reads, deploymentReads, writes atomic.Int32
}

func newRegressionResourceFixture(t *testing.T) (*regressionResourceFixture, *httptest.Server) {
	t.Helper()
	f := &regressionResourceFixture{appID: "app-1", deploymentAppID: "app-1", scope: "staging", native: regressionsFixture()}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodGet {
			f.writes.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var value any
		switch {
		case r.URL.Path == "/v1/apps/alpha":
			value = api.AppResponse{ID: f.appID, Slug: "alpha"}
		case r.URL.Path == "/v1/apps":
			value = []api.AppResponse{{ID: f.appID, Slug: "alpha"}, {ID: "app-other", Slug: "beta"}}
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/"):
			n := f.deploymentReads.Add(1)
			appID, scope := f.deploymentAppID, f.scope
			if n > 2 && f.afterDeploymentAppID != "" {
				appID = f.afterDeploymentAppID
			}
			if n > 2 && f.afterScope != "" {
				scope = f.afterScope
			}
			value = api.DeploymentResponse{ID: strings.TrimPrefix(r.URL.Path, "/v1/deployments/"), AppID: appID, Scope: scope}
		case r.URL.Path == "/v1/apps/alpha/debug/regressions":
			f.reads.Add(1)
			if r.URL.Query().Get("since") != "1h" || len(r.URL.Query()) != 1 {
				t.Error("resource changed its detection lookback or forwarded a projection bound")
			}
			if f.status != 0 {
				api.WriteProblem(w, api.NewProblem(f.status, "native_regression_error", "Unavailable", "native evidence denied"))
				return
			}
			value = f.native
			if f.afterAppID != "" {
				f.appID = f.afterAppID
			}
		default:
			t.Errorf("unexpected resource API read %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(b.Close)
	return f, b
}

func readRegressionResource(t *testing.T, h *subscriptionHarness) regressionResourceView {
	t.Helper()
	result, err := h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: regressionResourceURI})
	if err != nil || len(result.Contents) != 1 {
		t.Fatalf("regression resource=%+v %v", result, err)
	}
	var view regressionResourceView
	if json.Unmarshal([]byte(result.Contents[0].Text), &view) != nil {
		t.Fatal("invalid regression resource JSON")
	}
	return view
}

func awaitRegressionUpdate(t *testing.T, h *subscriptionHarness) {
	t.Helper()
	select {
	case update := <-h.updates:
		if update.URI != regressionResourceURI || update.Meta[mcp.MetaKeySubscriptionID] == nil {
			t.Fatalf("regression invalidation=%+v", update)
		}
		data, _ := json.Marshal(update)
		if strings.Contains(string(data), `"regressions":`) || strings.Contains(string(data), "app-1") || strings.Contains(string(data), "suspected_dependency") {
			t.Fatal("invalidation included resource contents")
		}
	case <-h.ctx.Done():
		t.Fatal("regression invalidation was not delivered")
	}
}

func TestRegressionResourceGuardedProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "app_denied", "foreign_after", "app_after", "scope_after", "bounded", "clamped", "empty", "native_error"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				f, b := newRegressionResourceFixture(t)
				opts := Options{APIBase: b.URL}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}
				}
				switch mode {
				case "denied":
					opts.Environments.RequestReadApps = nil
				case "replaced":
					opts.Environments.RequestReadApps["alpha"] = "old-app"
				case "app_denied":
					opts.AllowedApps = map[string]string{}
					opts.Environments.RequestReadApps = nil
				case "foreign_after":
					f.afterDeploymentAppID = "other-app"
				case "app_after":
					f.afterAppID = "replacement"
				case "scope_after":
					f.afterScope = "production"
				case "bounded":
					row := f.native.Regressions[0]
					f.native.Regressions = make([]api.DebugRegressionItem, api.MCPRegressionsDefault+1)
					for i := range f.native.Regressions {
						f.native.Regressions[i] = row
					}
					f.native.Regressions[api.MCPRegressionsDefault].DeploymentID = "excluded-unprojected"
				case "clamped":
					f.native.Since = "30m"
				case "empty":
					f.native.Regressions = nil
				case "native_error":
					f.status = http.StatusServiceUnavailable
				}
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: regressionResourceURI})
				denied := mode == "denied" || mode == "replaced" || mode == "app_denied"
				if denied || mode == "foreign_after" || mode == "app_after" || mode == "scope_after" || mode == "native_error" {
					if err == nil || result != nil {
						t.Fatal("resource released unconsented or unguarded diagnostics")
					}
					if denied && (f.reads.Load() != 0 || f.deploymentReads.Load() != 0) {
						t.Fatal("diagnostic reads occurred before consent")
					}
					if mode == "native_error" && !strings.Contains(err.Error(), "native_regression_error") {
						t.Fatal("native failure lost its code")
					}
					return
				}
				if err != nil || len(result.Contents) != 1 {
					t.Fatalf("resource=%+v %v", result, err)
				}
				content := result.Contents[0]
				var v regressionResourceView
				if json.Unmarshal([]byte(content.Text), &v) != nil || v.AppID != "app-1" || v.Scope != "app_wide" || v.LookbackBasis != "last_detected_at" || v.SourceCompleteness != "unknown" {
					t.Fatal("resource lost safe projection metadata")
				}
				if _, err := time.Parse(time.RFC3339Nano, v.SnapshotObservedAt); err != nil || !strings.Contains(v.UpdateSemantics, "does not prove recovery") || !strings.Contains(v.UpdateSemantics, "reconnect") {
					t.Fatal("resource omitted observation time or absence/resync semantics")
				}
				if strings.Contains(content.Text, "excluded-") || strings.Contains(content.Text, "fp_test_") || strings.Contains(content.Text, `"query"`) || content.URI != regressionResourceURI || content.MIMEType != "application/json" {
					t.Fatal("resource released excluded fields or lost its envelope")
				}
				if mode == "bounded" && (len(v.Regressions) != api.MCPRegressionsDefault || !v.RegressionsTruncated || f.deploymentReads.Load() != 2) {
					t.Fatal("resource exceeded the projected prefix or dereferenced an excluded deployment")
				}
				if mode == "clamped" && (!v.RetentionClamped || v.Since != "30m") || mode == "empty" && (v.Status != "no_visible_regressions" || v.Regressions == nil || len(v.Regressions) != 0) {
					t.Fatal("resource lost native retention/visibility semantics")
				}
				if f.reads.Load() != 1 || f.writes.Load() != 0 {
					t.Fatal("resource triggered polling or mutations")
				}
			})
		}
	}
}

func TestRegressionResourceURIsAndCompletion(t *testing.T) {
	f, b := newRegressionResourceFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}}, "2026-07-28")
	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, template := range templates.ResourceTemplates {
		found = found || template.URITemplate == regressionResourceTemplate && strings.Contains(template.Description, "app-wide")
	}
	if !found {
		t.Fatal("app-wide regression resource template was not discoverable")
	}
	for _, uri := range []string{"gregale://default/apps/alpha/regressions", "gregale://projects/shop/environments/staging/apps/alpha/regressions", "gregale://apps/alpha/regressions?since=24h", "gregale://apps/alpha/regressions#secret", "gregale://apps/alpha%2Fbeta/regressions", "gregale://apps/../regressions", "gregale://apps/alpha/regressions/", "gregale://apps/alpha/health"} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("accepted ambiguous resource %s", uri)
		}
	}
	params := &mcp.CompleteParams{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: regressionResourceTemplate}, Argument: mcp.CompleteParamsArgument{Name: "app", Value: ""}}
	result, err := cs.Complete(ctx, params)
	if err != nil || len(result.Completion.Values) != 1 || result.Completion.Values[0] != "alpha" {
		t.Fatalf("diagnostic completion=%+v %v", result, err)
	}
	params.Context = &mcp.CompleteContext{Arguments: map[string]string{"environment": "staging"}}
	if _, err := cs.Complete(ctx, params); err == nil {
		t.Fatal("completion implied environment-scoped regression evidence")
	}
	if f.reads.Load() != 0 || f.deploymentReads.Load() != 0 || f.writes.Load() != 0 {
		t.Fatal("URI rejection/completion fetched diagnostics or mutated native state")
	}
}

func TestRegressionSubscriptionsInvalidationAndAbsence(t *testing.T) {
	for _, httpTransport := range []bool{false, true} {
		name := "session"
		if httpTransport {
			name = "http"
		}
		t.Run(name, func(t *testing.T) {
			f, b := newRegressionResourceFixture(t)
			h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}}, "2026-07-28", httpTransport)
			h.subscribe(t, regressionResourceURI)
			select {
			case <-h.updates:
				t.Fatal("unchanged snapshot or collection clock emitted an update")
			case <-time.After(80 * time.Millisecond):
			}
			for _, change := range []string{"acknowledge", "detection", "disappear"} {
				f.mu.Lock()
				switch change {
				case "acknowledge":
					f.native.Regressions[0].State = "acknowledged"
					f.native.Regressions[0].AcknowledgedAt = "2026-10-17T11:00:00Z"
				case "detection":
					f.native.Regressions[0].LastDetectedAt = "2026-10-17T11:01:00Z"
				case "disappear":
					f.native.Regressions = nil
				}
				f.mu.Unlock()
				awaitRegressionUpdate(t, h)
				view := readRegressionResource(t, h)
				if change == "acknowledge" && view.Regressions[0].State != "acknowledged" || change == "detection" && view.Regressions[0].LastDetectedAt != "2026-10-17T11:01:00Z" || change == "disappear" && (view.Status != "no_visible_regressions" || len(view.Regressions) != 0 || !strings.Contains(view.UpdateSemantics, "does not prove recovery")) {
					t.Fatal("invalidation did not lead to a fresh, qualified snapshot")
				}
				select {
				case <-h.updates:
					t.Fatal("unchanged snapshot emitted duplicate invalidations")
				case <-time.After(60 * time.Millisecond):
				}
			}
			if err := h.client.Unsubscribe(h.ctx, &mcp.UnsubscribeParams{URI: regressionResourceURI}); err != nil {
				t.Fatal(err)
			}
			awaitResourceGroups(t, h, 0)
			if f.writes.Load() != 0 {
				t.Fatal("subscription performed native mutations")
			}
		})
	}
}

func TestRegressionSubscriptionsDenyBeforeAckAndSend(t *testing.T) {
	for _, mode := range []string{"no_consent", "replacement", "local_replacement", "native_error", "foreign_deployment"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newRegressionResourceFixture(t)
			opts := Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}}
			if mode == "no_consent" {
				opts.Environments.RequestReadApps = nil
			}
			if mode == "local_replacement" {
				opts.Environments = nil
			}
			h := connectSubscriptionHarness(t, b, opts, "2026-07-28", true)
			if mode == "no_consent" {
				// Subscribe starts the SDK's asynchronous listen. A rejected
				// admission must produce no acknowledgement, even if queuing
				// that listen itself returned successfully.
				if err := h.client.Subscribe(h.ctx, &mcp.SubscribeParams{URI: regressionResourceURI}); err != nil {
					t.Logf("listen rejected: %v", err)
				}
				select {
				case <-h.acks:
					t.Fatal("subscription acknowledged unconsented diagnostics")
				case <-time.After(80 * time.Millisecond):
				}
				awaitResourceGroups(t, h, 0)
				if f.reads.Load() != 0 {
					t.Fatal("subscription inspected unconsented diagnostics")
				}
				return
			}
			h.subscribe(t, regressionResourceURI)
			f.mu.Lock()
			switch mode {
			case "replacement", "local_replacement":
				f.appID = "recreated"
			case "native_error":
				f.status = http.StatusServiceUnavailable
			case "foreign_deployment":
				f.deploymentAppID = "other-app"
			}
			f.mu.Unlock()
			// Invoke the publisher directly so recipient checks cannot be
			// replaced by trusting a previous successful polling read.
			if err := h.manager.resourceServer.ResourceUpdated(h.ctx, &mcp.ResourceUpdatedNotificationParams{URI: regressionResourceURI}); err != nil {
				t.Fatal(err)
			}
			awaitResourceGroups(t, h, 0)
			select {
			case <-h.updates:
				t.Fatal("failed authorization/inspection emitted an invalidation")
			default:
			}
			if (mode == "replacement" || mode == "local_replacement") && f.reads.Load() != 1 {
				t.Fatal("recreated app inherited diagnostic polling")
			}
		})
	}
}

func TestRegressionSubscriptionsLegacyAndReconnect(t *testing.T) {
	f, b := newRegressionResourceFixture(t)
	legacy := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2025-11-25", true)
	if legacy.client.InitializeResult().Capabilities.Resources.Subscribe {
		t.Fatal("legacy subscription support advertised")
	}
	readRegressionResource(t, legacy)
	if err := legacy.client.Subscribe(legacy.ctx, &mcp.SubscribeParams{URI: regressionResourceURI}); err == nil {
		t.Fatal("legacy regression subscription accepted")
	}
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	h.subscribe(t, regressionResourceURI)
	_ = h.client.Close()
	awaitResourceGroups(t, h, 0)
	f.mu.Lock()
	f.native.Regressions = nil
	f.mu.Unlock()
	fresh := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	if view := readRegressionResource(t, fresh); view.Status != "no_visible_regressions" {
		t.Fatal("reconnecting client did not see current visibility")
	}
	if activeResourceGroups(fresh.manager) != 0 {
		t.Fatal("read automatically restarted the prior listen")
	}
	fresh.subscribe(t, regressionResourceURI)
	f.mu.Lock()
	f.native = regressionsFixture()
	f.mu.Unlock()
	awaitRegressionUpdate(t, fresh)
}

func TestRegressionSubscriptionCancelsInFlightRead(t *testing.T) {
	_, native := newRegressionResourceFixture(t)
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/debug/regressions" && calls.Add(1) == 2 {
			close(entered)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		native.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	h.subscribe(t, regressionResourceURI)
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("subscription did not start its next diagnostic read")
	}
	_ = h.client.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnect left a diagnostic read in flight")
	}
	awaitResourceGroups(t, h, 0)
}

func TestRegressionResourceDigestTracksDetectionAndTriage(t *testing.T) {
	view := regressionResourceView{regressionsView: regressionsView{Regressions: []regressionView{{LastDetectedAt: "2026-10-17T10:00:00Z", State: "active"}}}, SnapshotObservedAt: "2026-10-17T10:00:01Z"}
	digest := func() [32]byte {
		data, _ := json.Marshal(view)
		d, err := resourceSemanticDigest(data, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	initial := digest()
	view.SnapshotObservedAt = "2026-10-17T10:00:02Z"
	if digest() != initial {
		t.Fatal("collection clock changed digest")
	}
	view.Regressions[0].LastDetectedAt = "2026-10-17T10:00:02Z"
	detection := digest()
	if detection == initial {
		t.Fatal("new detection timestamp did not change digest")
	}
	view.Regressions[0].State = "acknowledged"
	view.Regressions[0].AcknowledgedAt = "2026-10-17T10:00:03Z"
	triage := digest()
	if triage == detection {
		t.Fatal("triage change did not change digest")
	}
	view.Regressions[0].AcknowledgedAt = "2026-10-17T10:00:04Z"
	if digest() == triage {
		t.Fatal("triage timestamp alone did not change digest")
	}
}

func TestRegressionResourceDigestPreservesLargeCounts(t *testing.T) {
	now := time.Now()
	before, err := resourceSemanticDigest(json.RawMessage(`{"regressions":[{"affected_count":9007199254740992}]}`), now)
	if err != nil {
		t.Fatal(err)
	}
	after, err := resourceSemanticDigest(json.RawMessage(`{"regressions":[{"affected_count":9007199254740993}]}`), now)
	if err != nil || before == after {
		t.Fatal("change digest rounded distinct integer counters together")
	}
}
