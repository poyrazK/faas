package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRemoteEnvironmentConsentSurvivesRestartWithoutWidening(t *testing.T) {
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "api-key", StateStore: store})
	flow := f.begin(scopeRead + " " + scopeWrite)
	response := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("account review failed")
	}
	form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {"selected"}, "apps": {"alpha"}, "allow_writes": {"yes"}, "environments": {"shop/staging", "shop/production"}, "environment_writes": {"shop/staging"}, "request_read_apps": {"alpha"}, "regression_write_apps": {"alpha"}}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/oauth/consent", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", f.server.URL)
	response, err = flow.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("consent=%d", response.StatusCode)
	}
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var tokens tokenResponse
	response = f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	check := func() {
		cs, ctx := f.connect(tokens, "2026-07-28")
		defer cs.Close()
		result := callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
		var consent EnvironmentConsent
		if err := json.Unmarshal(result["environment_access"], &consent); err != nil {
			t.Fatal(err)
		}
		if consent.RegressionWriteApps["alpha"] != "alice-app" || consent.RequestReadApps["alpha"] != "alice-app" || consent.DefaultWrite || len(consent.Named) != 2 || !consent.Named["shop/staging"].Write || consent.Named["shop/production"].Write || consent.Named["shop/production"].EnvironmentID != "alice-production" {
			t.Fatal("reviewed environment consent widened or disappeared")
		}
		result = callTool(t, ctx, cs, "gregale_list_environments", map[string]any{}, false)
		if !strings.Contains(string(result["items"]), "alice-staging") {
			t.Fatal("named environment access was lost")
		}
		result = callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": "unknown"}, true)
		if string(result["code"]) != `"environment_access_denied"` {
			t.Fatal("named writes silently enabled default writes")
		}
	}
	check()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	f.opts.StateStore = next
	h, err := NewHTTPHandler(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(h)
	check()
}

func testStateStore(t *testing.T) (*StateStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key, path := filepath.Join(dir, "key"), filepath.Join(dir, "state")
	if err := os.WriteFile(key, bytes.Repeat([]byte{27}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path, key
}

func TestManagementEncryptedStateLockTamperAndPermissions(t *testing.T) {
	store, path, key := testStateStore(t)
	if second, err := OpenStateStore(path, key); err == nil {
		_ = second.Close()
		t.Fatal("second writer acquired state")
	}
	if err := store.update("test", map[string]string{"credential": testToken}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte(testToken)) {
		t.Fatal("state leaked credential")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStateStore(path, key); err == nil {
		_ = s.Close()
		t.Fatal("tampered state accepted")
	}
	if err := os.Chmod(key, 0644); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStateStore(path, key); err == nil {
		_ = s.Close()
		t.Fatal("public key file accepted")
	}
}

func TestRemoteOAuthSurvivesRestartRotationAndRevocation(t *testing.T) {
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "api-key", StateStore: store})
	code := f.authorizeAccount("fp_test_alice_remote_key", true)
	var tokens tokenResponse
	response := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	cs, ctx := f.connect(tokens, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "restart test"}, false)
	id := planID(t, plan)
	_ = cs.Close()
	restart := func() {
		if err := f.opts.StateStore.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := OpenStateStore(path, key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = next.Close() })
		f.opts.StateStore = next
		h, err := NewHTTPHandler(f.opts)
		if err != nil {
			t.Fatal(err)
		}
		f.setHandler(h)
	}
	restart()
	cs, ctx = f.connect(tokens, "2026-07-28")
	callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
	// Retained exact plan is recognized; it has not staged yet.
	result := callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, true)
	if !strings.Contains(string(result["code"]), "not_staged") {
		t.Fatal("release plan lost across restart")
	}
	_ = cs.Close()
	refreshBody := "grant_type=refresh_token&client_id=" + f.clientID + "&refresh_token=" + tokens.RefreshToken
	var rotated tokenResponse
	response = f.request("POST", "/oauth/token", strings.NewReader(refreshBody), "application/x-www-form-urlencoded", "")
	decodeResponse(t, response, &rotated)
	_ = response.Body.Close()
	restart()
	replay := f.request("POST", "/oauth/token", strings.NewReader(refreshBody), "application/x-www-form-urlencoded", "")
	if replay.StatusCode != 400 {
		t.Fatal("refresh replay survived restart")
	}
	_ = replay.Body.Close()
	denied := f.request("POST", "/mcp", strings.NewReader(`{}`), "application/json", rotated.AccessToken)
	if denied.StatusCode != 401 {
		t.Fatal("refresh replay did not revoke grant")
	}
	_ = denied.Body.Close()
	restart()
	denied = f.request("POST", "/mcp", strings.NewReader(`{}`), "application/json", rotated.AccessToken)
	if denied.StatusCode != 401 {
		t.Fatal("revoked grant resurrected")
	}
	_ = denied.Body.Close()
}

func TestManagementAmbiguousDeployRetryAfterRestart(t *testing.T) {
	store, path, key := testStateStore(t)
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "grant-one"}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	output, err := m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Image: testImage, Reason: "restart after ambiguous commit"})
	if err != nil {
		t.Fatal(err)
	}
	id := output.(releasePlan).ID
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("fixture did not return ambiguous failure")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }()
	opts.planStore = next
	_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatalf("restart duplicated write: %d", f.writes)
	}
}

func TestManagementPersistenceFailurePreventsAPIMutation(t *testing.T) {
	store, path, _ := testStateStore(t)
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, AllowWrites: true, planStore: store, planNamespace: "one"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := m.planDeploy(context.Background(), planDeployArgs{App: "alpha", Image: testImage, Reason: "storage failure"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(dir+"-moved", dir) }()
	if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: output.(releasePlan).ID}); err == nil {
		t.Fatal("write passed failed persistence")
	}
	if _, err := m.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: output.(releasePlan).ID}); err == nil {
		t.Fatal("history exposed an attempt that could not be committed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 || store.available() {
		t.Fatal("failure did not disable mutations")
	}
}

func TestRemoteNativeOnboarding(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser"})
	flow := f.begin(scopeRead)
	denied := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
	if denied.StatusCode != 400 {
		t.Fatal("browser mode accepted API-key onboarding")
	}
	_ = denied.Body.Close()
	for _, decision := range []string{"start-login", "check-login"} {
		reply := f.consent(flow, decision, "", f.server.URL, flow.csrf, false)
		data, err := io.ReadAll(reply.Body)
		_ = reply.Body.Close()
		if err != nil || reply.StatusCode != 200 {
			t.Fatalf("native step=%d", reply.StatusCode)
		}
		if bytes.Contains(data, []byte("fp_test_alice_remote_key")) {
			t.Fatal("browser page exposed issued credential")
		}
		if decision == "check-login" && !bytes.Contains(data, []byte("Connect this account")) {
			t.Fatal("native login skipped account review")
		}
	}
	reply := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false, "alpha")
	if reply.StatusCode != 303 {
		t.Fatalf("native consent=%d", reply.StatusCode)
	}
	_ = reply.Body.Close()
	redirect, err := url.Parse(reply.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var tokens tokenResponse
	response := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	cs, ctx := f.connect(tokens, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
	if string(result["read_only"]) != "true" || !strings.Contains(string(result["allowed_apps"]), "alpha") {
		t.Fatal("native grant lost reviewed permissions")
	}
	callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, false)
	callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "beta"}, true)
}
