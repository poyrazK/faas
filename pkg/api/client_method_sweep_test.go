package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file drives every 0%-coverage method on *Client. Each test
// boots an httptest.NewServer, wires a NewClient against it, and
// exercises the SDK wire path. Bodies are intentionally minimal —
// coverage, not type-perfect DTOs.

func newSweepServer(t *testing.T, status int, body string) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestSweep_PostAccountMfaEnroll(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"otpauth_url":"otpauth://x","recovery_codes":["a","b"]}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.PostAccountMfaEnroll(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_PostAccountMfaConfirm(t *testing.T) {
	srv, captured := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.PostAccountMfaConfirm(context.Background(), MFAConfirmRequest{Totp: "123456"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(*captured) == 0 {
		t.Error("expected request body")
	}
}

func TestSweep_PostAccountMfaVerify(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.PostAccountMfaVerify(context.Background(), MFAVerifyRequest{Totp: "111111"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_PostAccountMfaRecover(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.PostAccountMfaRecover(context.Background(), MFARecoverRequest{Code: "RC-1234"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_PostAccountMfaDisable(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.PostAccountMfaDisable(context.Background(), MFADisableRequest{Password: "pw"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_PostAccountLogout(t *testing.T) {
	srv, _ := newSweepServer(t, 204, ``)
	c := NewClient(srv.URL, "fp_test")
	if err := c.PostAccountLogout(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_Deploy(t *testing.T) {
	srv, captured := newSweepServer(t, 200, `{"id":"dep_1"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.Deploy(context.Background(), "myapp", CreateDeploymentRequest{Image: "reg/foo"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(*captured) == 0 {
		t.Error("expected request body")
	}
}

func TestSweep_GetDeploymentScan(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"status":"complete"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetDeploymentScan(context.Background(), "dep_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_PatchDeployment(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"id":"dep_1","min_instances":2}`)
	c := NewClient(srv.URL, "fp_test")
	min := 2
	if _, err := c.PatchDeployment(context.Background(), "dep_1", UpdateDeploymentRequest{MinInstances: &min}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_GetBuildsIdProvenance(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"build_id":"b1"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetBuildsIdProvenance(context.Background(), "b1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_GetBuildsIdSbom(t *testing.T) {
	body := `{"bomFormat":"CycloneDX","specVersion":"1.5"}`
	srv, _ := newSweepServer(t, 200, body)
	c := NewClient(srv.URL, "fp_test")
	got, err := c.GetBuildsIdSbom(context.Background(), "b1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q", got)
	}
}

func TestSweep_GetBuildsId(t *testing.T) {
	// DEPLOY-PROV-6 / ADR-089 (issue #741): the /v1/builds/{id}
	// lifecycle surface. Body pins the field set so the
	// json.Unmarshal-side stays covered by the sweep — when the
	// DTO adds a field, this test pins the wire name as the
	// sweep-server mock so a rename doesn't silently deserialise
	// into the zero value.
	body := `{"id":"b1","deployment_id":"d1","kind":"railpack","source_bytes":12345,"status":"running","enqueued_at":"2026-08-10T12:34:56Z","started_at":"2026-08-10T12:34:58Z"}`
	srv, _ := newSweepServer(t, 200, body)
	c := NewClient(srv.URL, "fp_test")
	got, err := c.GetBuildsId(context.Background(), "b1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.ID != "b1" || got.Status != "running" || got.Kind != "railpack" {
		t.Errorf("got = %+v", got)
	}
}

func TestSweep_GetBuilds(t *testing.T) {
	// DEPLOY-PROV-6 follow-up / ADR-091 (issue #741 close-out):
	// the /v1/builds list surface. Body pins the page envelope
	// (items + next_before) so the JSON deserialise side stays
	// covered by the sweep. The query-string encoding path is
	// exercised implicitly via NewClient.do — the test pins the
	// happy-path round-trip, not the URL shape (sdk-coverage
	// gate covers the latter).
	body := `{"items":[{"id":"b1","deployment_id":"d1","kind":"railpack","source_bytes":12345,"status":"running","enqueued_at":"2026-08-10T12:34:56Z","started_at":"2026-08-10T12:34:58Z"}],"next_before":"2026-08-10T12:34:58Z"}`
	srv, _ := newSweepServer(t, 200, body)
	c := NewClient(srv.URL, "fp_test")
	got, err := c.GetBuilds(context.Background(), "myapp", "running", "", 50)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "b1" {
		t.Errorf("items = %+v", got.Items)
	}
	if got.NextBefore != "2026-08-10T12:34:58Z" {
		t.Errorf("NextBefore = %q", got.NextBefore)
	}
}

func TestSweep_GetEgressAllowlistExtra(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetEgressAllowlistExtra(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_SetEgressAllowlistExtra(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.SetEgressAllowlistExtra(context.Background(), 5); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_GetInstances(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"items":[],"cursor":""}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetInstances(context.Background(), "", 50); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_CreateCron(t *testing.T) {
	srv, _ := newSweepServer(t, 201, `{"id":"cron_1","schedule":"*/5 * * * *"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.CreateCron(context.Background(), "myapp", CreateCronRequest{Schedule: "*/5 * * * *"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_ListAlertRules(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `[]`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.ListAlertRules(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_CreateAlertRule(t *testing.T) {
	srv, _ := newSweepServer(t, 201, `{"id":"ar_1"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.CreateAlertRule(context.Background(), "myapp", CreateAlertRuleRequest{Name: "x"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_GetAlertRule(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"id":"ar_1","name":"x"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetAlertRule(context.Background(), "myapp", "ar_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_UpdateAlertRule(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"id":"ar_1","name":"y"}`)
	c := NewClient(srv.URL, "fp_test")
	name := "y"
	if _, err := c.UpdateAlertRule(context.Background(), "myapp", "ar_1", UpdateAlertRuleRequest{Name: &name}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_DeleteAlertRule(t *testing.T) {
	srv, _ := newSweepServer(t, 204, ``)
	c := NewClient(srv.URL, "fp_test")
	if err := c.DeleteAlertRule(context.Background(), "myapp", "ar_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_RotateAlertRuleSecret(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"webhook_secret":"newsec"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.RotateAlertRuleSecret(context.Background(), "myapp", "ar_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_InvokeApp(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"status":"ok"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.InvokeApp(context.Background(), "myapp", InvokeRequest{Method: "GET", Path: "/"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_InvokeAppAsync(t *testing.T) {
	srv, _ := newSweepServer(t, 202, `{"id":"inv_1"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.InvokeAppAsync(context.Background(), "myapp", InvokeRequest{Method: "POST", Path: "/q"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_QueueSend(t *testing.T) {
	srv, _ := newSweepServer(t, 202, `{"id":"q_1"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.QueueSend(context.Background(), "myapp", QueueSendRequest{Payload: json.RawMessage(`"msg"`)}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_QueueReceive(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"items":[{"id":"q_1","body":"msg"}]}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.QueueReceive(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_QueueAck(t *testing.T) {
	srv, _ := newSweepServer(t, 204, ``)
	c := NewClient(srv.URL, "fp_test")
	if err := c.AckQueueRow(context.Background(), "myapp", "q_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_QueuePeek(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"items":[]}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.QueuePeek(context.Background(), "myapp", 5, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_QueueDeadLetter(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"items":[]}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.QueueDeadLetter(context.Background(), "myapp", 5, ""); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_DeleteApp(t *testing.T) {
	srv, _ := newSweepServer(t, 204, ``)
	c := NewClient(srv.URL, "fp_test")
	if err := c.DeleteApp(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_Park(t *testing.T) {
	srv, _ := newSweepServer(t, 202, `{"ok":true}`)
	c := NewClient(srv.URL, "fp_test")
	if err := c.Park(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_Wake(t *testing.T) {
	srv, _ := newSweepServer(t, 202, `{"ok":true}`)
	c := NewClient(srv.URL, "fp_test")
	if err := c.Wake(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_GetInvocation(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"id":"inv_1","status":"done"}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.GetInvocation(context.Background(), "inv_1"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_Rollback(t *testing.T) {
	srv, _ := newSweepServer(t, 202, `{"ok":true}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.Rollback(context.Background(), "myapp"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_UsageDaily(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"days":[]}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.UsageDaily(context.Background(), "2026-08"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_StorageUsage(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"by_app":{}}`)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.StorageUsage(context.Background(), "2026-08"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_ExportAccountFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("include_secrets") != "false" {
			t.Errorf("query = %q, want include_secrets=false", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.ExportAccount(context.Background(), false); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_ExportAccountTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.ExportAccount(context.Background(), true); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_DeleteAccountExplicitKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "key-stable" {
			t.Errorf("Idempotency-Key = %q, want key-stable", r.Header.Get("Idempotency-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]string{"scheduled_for": "2026-09"})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.DeleteAccount(context.Background(), "key-stable"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSweep_DoMarshalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "fp_test")
	err := c.do(context.Background(), "POST", "/x", make(chan int), nil)
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "marshal") {
		t.Errorf("err = %v, want marshal error", err)
	}
}

// Issue #311 — programmatic auth sweep coverage. The three new
// client methods round-trip the wire shape end-to-end through an
// httptest server.
func TestSweep_PostV1AuthSignup(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"account_id":"acc_X","email":"alice@example.com","plan":"free","api_key":{"plaintext":"fp_live_abc123","prefix":"fp_live_","id":"key_01HXYZ"}}`)
	c := NewClient(srv.URL, "fp_test")
	resp, err := c.PostAuthSignup(context.Background(), "alice@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.AccountID != "acc_X" {
		t.Errorf("account_id = %q, want acc_X", resp.AccountID)
	}
	if resp.APIKey.Plaintext != "fp_live_abc123" {
		t.Errorf("api_key.plaintext = %q, want fp_live_abc123", resp.APIKey.Plaintext)
	}
}

func TestSweep_PostV1AuthLogin(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{"account_id":"acc_Y","email":"bob@example.com","plan":"hobby","api_key":{"plaintext":"fp_live_def456","prefix":"fp_live_","id":"key_01HABC"}}`)
	c := NewClient(srv.URL, "fp_test")
	resp, err := c.PostAuthLogin(context.Background(), "bob@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.APIKey.ID != "key_01HABC" {
		t.Errorf("api_key.id = %q, want key_01HABC", resp.APIKey.ID)
	}
}

func TestSweep_PostV1AuthSignupMagicLink(t *testing.T) {
	srv, _ := newSweepServer(t, 200, `{}`)
	c := NewClient(srv.URL, "fp_test")
	if err := c.PostAuthSignupMagicLink(context.Background(), "carol@example.com"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// (unused — keep json import alive for the r/t field below)
var _ = json.Unmarshal
