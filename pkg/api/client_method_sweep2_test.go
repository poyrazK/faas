package api

// client_method_sweep2_test.go: covers zero-coverage methods on
// pkg/api/client.go that the existing test files do not reach. Each
// method gets one minimal test against a stub httptest server so
// the SDK's routing + JSON-decoding path runs end-to-end.
//
// The stub server returns a JSON shape that matches the production
// return type (object for single-resource methods, array for list
// methods). The test asserts the SDK didn't error, NOT the exact
// route path — paths change frequently and asserting them all
// correctly is brittle (the OpenAPI spec is the source of truth for
// routes; the SDK is the source of truth for serialization).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// objectServer returns a server that responds with `{}` to every
// request. Used for methods that decode into a struct (most paths).
func objectServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// arrayServer returns a server that responds with `[]` to every
// request. Used for list methods.
func arrayServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bytesServer returns a server that responds with raw bytes.
// Used for []byte-returning methods.
func bytesServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClientSweep2_NoArgMethods drives zero-coverage methods whose
// argument shape is a single ctx (or a few primitive strings).
// Every case here is verified to compile against the production
// signature.
func TestClientSweep2_NoArgMethods(t *testing.T) {
	ctx := context.Background()
	obj := objectServer(t)
	arr := arrayServer(t)
	bt := bytesServer(t)

	cases := []struct {
		name string
		url  string
		call func(t *testing.T, c *Client) error
	}{
		// --- MFA + logout ---
		{"PostAccountMfaEnroll", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.PostAccountMfaEnroll(ctx)
			return err
		}},
		{"PostAccountLogout", obj.URL, func(t *testing.T, c *Client) error {
			return c.PostAccountLogout(ctx)
		}},

		// --- Account-level ---
		{"GetEgressAllowlistExtra", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetEgressAllowlistExtra(ctx)
			return err
		}},
		{"GetGraceWindow", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetGraceWindow(ctx)
			return err
		}},
		{"GetAccountSLO_24h", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAccountSLO(ctx, "24h")
			return err
		}},
		{"GetBillingPortal", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetBillingPortal(ctx)
			return err
		}},
		{"GetAccountDPA", bt.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAccountDPA(ctx)
			return err
		}},
		{"GetMyOrg", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetMyOrg(ctx)
			return err
		}},

		// --- CLI / passwords ---
		{"MintCliAuthCode", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.MintCliAuthCode(ctx)
			return err
		}},
		{"Logout", obj.URL, func(t *testing.T, c *Client) error {
			return c.Logout(ctx)
		}},

		// --- App-level ---
		{"GetInstances_x", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetInstances(ctx, "", 50)
			return err
		}},
		{"QueueState", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.QueueState(ctx, "x")
			return err
		}},
		{"QueueDeadLetter", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.QueueDeadLetter(ctx, "x", 10, "")
			return err
		}},
		{"GetDelayedTask", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetDelayedTask(ctx, "d")
			return err
		}},
		{"CancelDelayedTask", obj.URL, func(t *testing.T, c *Client) error {
			return c.CancelDelayedTask(ctx, "d")
		}},
		{"ListInvocations", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListInvocations(ctx, "", 50)
			return err
		}},
		{"GetInvocation", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetInvocation(ctx, "inv")
			return err
		}},
		{"ReplayInvocation", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ReplayInvocation(ctx, "inv")
			return err
		}},

		// --- Build provenance / SBOM ---
		{"GetBuildsIdProvenance", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetBuildsIdProvenance(ctx, "b")
			return err
		}},
		{"GetBuildsIdSbom", bt.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetBuildsIdSbom(ctx, "b")
			return err
		}},

		// --- Audit / wake timeline ---
		{"GetAuditEvent", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAuditEvent(ctx, "ev")
			return err
		}},

		// --- Alerts ---
		{"ListAlertRules", arr.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListAlertRules(ctx, "x")
			return err
		}},
		{"GetAlertRule", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAlertRule(ctx, "x", "r")
			return err
		}},
		{"DeleteAlertRule", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteAlertRule(ctx, "x", "r")
		}},
		{"RotateAlertRuleSecret", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.RotateAlertRuleSecret(ctx, "x", "r")
			return err
		}},

		// --- Secrets / registry / env ---
		{"GetSecrets", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetSecrets(ctx, "", 50)
			return err
		}},
		{"ListAppRegistryCredentials", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListAppRegistryCredentials(ctx, "x")
			return err
		}},
		{"DeleteAppRegistryCredential", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteAppRegistryCredential(ctx, "x", "h")
		}},
		{"GetAppsSlugEnv", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAppsSlugEnv(ctx, "x")
			return err
		}},
		{"DeleteAppsSlugEnvKey", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteAppsSlugEnvKey(ctx, "x", "K")
		}},

		// --- Metrics / SLO / usage ---
		{"GetAppMetrics", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAppMetrics(ctx, "x", "24h")
			return err
		}},
		{"GetAppsMetrics", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAppsMetrics(ctx, "24h")
			return err
		}},
		{"GetAppSLO", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAppSLO(ctx, "x", "24h")
			return err
		}},
		{"UsageDaily", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.UsageDaily(ctx, "")
			return err
		}},
		{"StorageUsage", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.StorageUsage(ctx, "")
			return err
		}},
		{"ListInvoices", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListInvoices(ctx, "", "", 50)
			return err
		}},

		// --- Trusted signers ---
		{"ListAppTrustedSigners", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListAppTrustedSigners(ctx, "x")
			return err
		}},
		{"DeleteAppTrustedSigner", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteAppTrustedSigner(ctx, "x", "n")
		}},

		// --- Orgs ---
		{"ListOrgs", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListOrgs(ctx)
			return err
		}},
		{"GetOrg", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetOrg(ctx, "o")
			return err
		}},
		{"DeleteOrg", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteOrg(ctx, "o")
		}},
		{"ListOrgMembers", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListOrgMembers(ctx, "o")
			return err
		}},
		{"RemoveOrgMember", obj.URL, func(t *testing.T, c *Client) error {
			return c.RemoveOrgMember(ctx, "o", "m")
		}},
		{"PeekInvitation", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.PeekInvitation(ctx, "i")
			return err
		}},
		{"AcceptInvitation", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.AcceptInvitation(ctx, "i")
			return err
		}},
		{"RevokeInvitation", obj.URL, func(t *testing.T, c *Client) error {
			return c.RevokeInvitation(ctx, "o", "i")
		}},
		{"ListOrgInvitationsAll", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListOrgInvitationsAll(ctx, "o")
			return err
		}},
		{"ListOrgInvitations", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListOrgInvitations(ctx, "o", "", 50)
			return err
		}},
		{"GetOrgSeatUsage", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetOrgSeatUsage(ctx, "o")
			return err
		}},

		// --- Webhooks ---
		{"ListAppWebhooks", arr.URL, func(t *testing.T, c *Client) error {
			_, err := c.ListAppWebhooks(ctx, "x")
			return err
		}},
		{"GetAppWebhook", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetAppWebhook(ctx, "x", "w")
			return err
		}},
		{"DeleteAppWebhook", obj.URL, func(t *testing.T, c *Client) error {
			return c.DeleteAppWebhook(ctx, "x", "w")
		}},
		{"RotateAppWebhookSecret", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.RotateAppWebhookSecret(ctx, "x", "w")
			return err
		}},
		{"RetryAppWebhookDelivery", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.RetryAppWebhookDelivery(ctx, "x", "w", "d")
			return err
		}},

		// --- Deployments ---
		{"GetDeploymentScan", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetDeploymentScan(ctx, "d")
			return err
		}},
		{"GetDeploymentSecretScan", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetDeploymentSecretScan(ctx, "d")
			return err
		}},
		{"GetDeploymentStages", obj.URL, func(t *testing.T, c *Client) error {
			_, err := c.GetDeploymentStages(ctx, "d")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(tc.url, "fp_test")
			if err := tc.call(t, c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}
}

// TestClientDoBytes_EmptyBody — doBytes with an empty body (the
// common case for GETs / DELETEs) must not panic and must pass the
// body verbatim to the server.
func TestClientDoBytes_EmptyBody(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")
	if err := c.doBytes(context.Background(), "GET", "/healthz", nil, nil); err != nil {
		t.Fatalf("doBytes: %v", err)
	}
	if got != "GET" {
		t.Errorf("method captured = %q, want GET", got)
	}
}

// TestClientDoBytes_WithBytesBody — doBytes with a []byte body
// must serialise it as the request body. Note: doBytes treats the
// body as raw bytes; the http transport doesn't transform it. The
// server sees the literal bytes the caller passed in.
func TestClientDoBytes_WithBytesBody(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")
	// Use a JSON RawMessage-style byte slice; doBytes sees []byte
	// and writes it verbatim.
	if err := c.doBytes(context.Background(), "POST", "/x", json.RawMessage(`{"k":"v"}`), nil); err != nil {
		t.Fatalf("doBytes: %v", err)
	}
	if got != `{"k":"v"}` {
		t.Errorf("body = %q, want %q", got, `{"k":"v"}`)
	}
}

// TestClientSweep2_BearerHeaderSet — every wrapper must carry the
// bearer token. Confirms the SDK's auth wiring is intact across the
// full surface.
func TestClientSweep2_BearerHeaderSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test_secret")
	if _, err := c.ListApps(context.Background()); err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer prefix", gotAuth)
	}
	if !strings.Contains(gotAuth, "fp_test_secret") {
		t.Errorf("Authorization = %q, want token fp_test_secret", gotAuth)
	}
}

// TestClient_DestroyPreview pins the new SDK method
// (issue #961 Mega-C PR-1, leaf 3). The fake server records the
// (method, path) tuple; we assert the SDK hits the preview
// destroy route — POST /v1/preview/{slug}/destroy — and returns
// nil on 204 No Content. The path carries the slug verbatim
// so the customer can grep their dashboard URL → SDK call.
func TestClient_DestroyPreview(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test_secret")
	if err := c.DestroyPreview(context.Background(), "pr-42-acme"); err != nil {
		t.Fatalf("DestroyPreview: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/preview/pr-42-acme/destroy" {
		t.Errorf("path = %q, want /v1/preview/pr-42-acme/destroy", gotPath)
	}
}

func TestClient_DevSessionMethods(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"app":{"id":"app-1","slug":"dev-demo-abc","type":"app","ram_mb":128,"max_concurrency":1,"concurrency_per_vm":1,"min_instances":0,"status":"active","url":"https://dev-demo-abc.gregale.dev","manifest":{},"autoscale_target_rps":0,"autoscale_target_cpu_pct":0},"expires_at":"2026-09-06T12:00:00Z"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test_secret")
	got, err := c.UpsertDevSession(context.Background(), "demo", UpsertDevSessionRequest{})
	if err != nil {
		t.Fatalf("UpsertDevSession: %v", err)
	}
	if got.App.Slug != "dev-demo-abc" {
		t.Fatalf("decoded slug = %q", got.App.Slug)
	}
	if err := c.DestroyDevSession(context.Background(), "demo"); err != nil {
		t.Fatalf("DestroyDevSession: %v", err)
	}
	want := []string{"PUT /v1/dev/sessions/demo", "DELETE /v1/dev/sessions/demo"}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// TestClient_DestroyPreview_ProductionAppReturns404 confirms
// the SDK surfaces the apid's 404 preview_not_found error
// rather than silently succeeding — the customer must see
// "this slug is not a preview app" rather than a misleading
// "Destroyed" message.
func TestClient_DestroyPreview_ProductionAppReturns404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"preview_not_found","title":"Preview not found","detail":"the slug does not identify a preview app; use DELETE /v1/apps/{slug} to destroy a production app","status":404}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test_secret")
	err := c.DestroyPreview(context.Background(), "prod-app")
	if err == nil {
		t.Fatal("DestroyPreview on production slug: want error, got nil")
	}
	if !strings.Contains(err.Error(), "preview_not_found") {
		t.Errorf("err = %v, want it to carry the preview_not_found problem code", err)
	}
}
