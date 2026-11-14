package support

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func TestWebhookSignatureMetadataAndRedirectRefusal(t *testing.T) {
	secret := strings.Repeat("s", 32)
	received := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received++
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(r.Header.Get("X-Gregale-Timestamp") + "."))
		_, _ = mac.Write(body)
		if r.Header.Get("X-Gregale-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature mismatch")
		}
		if r.Header.Get("X-Gregale-Event-ID") != "event" || strings.Contains(string(body), "body") || strings.Contains(string(body), "subject") {
			t.Errorf("unsafe envelope %s", body)
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	hook, e := NewWebhook(target.URL, secret)
	if e != nil {
		t.Fatal(e)
	}
	hook.client = target.Client()
	hook.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if e = hook.send(t.Context(), "event", "ticket", "reply"); e != nil {
		t.Fatal(e)
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	hook.endpoint = redirect.URL
	hook.client = redirect.Client()
	hook.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if e = hook.send(t.Context(), "event", "ticket", "reply"); e == nil {
		t.Fatal("redirect was accepted")
	}
	if received != 1 {
		t.Fatal("redirect reached its destination")
	}
	for _, tc := range []struct{ url, secret string }{{"http://example.com", secret}, {"https://example.com", "short"}, {"https://user:pass@example.com", secret}, {"", secret}} {
		if _, e = NewWebhook(tc.url, tc.secret); e == nil {
			t.Errorf("invalid configuration accepted: %s", tc.url)
		}
	}
}
func TestWebhookLeaseFencesStaleCompletionAndRetries(t *testing.T) {
	f := supportDB(t)
	sent := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent++; w.WriteHeader(500) }))
	defer target.Close()
	hook, _ := NewWebhook(target.URL, strings.Repeat("s", 32))
	hook.client = target.Client()
	f.s.Webhook = hook
	f.now = time.Now().Add(-time.Minute).UTC()
	ticket := f.create(t, "secret body must stay local")
	queries := q.New()
	claim, e := queries.OutboxClaim(t.Context(), f.pool, q.OutboxClaimParams{AvailableAt: ts(time.Now()), LeaseID: uid(newID()), LeaseUntil: ts(time.Now().Add(-time.Second))})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.dispatch(t.Context()); e != nil {
		t.Fatal(e)
	}
	if sent != 1 {
		t.Fatalf("send attempts %d", sent)
	}
	if e = queries.OutboxFinish(t.Context(), f.pool, q.OutboxFinishParams{ID: claim.ID, LeaseID: claim.LeaseID, State: "accepted", AvailableAt: ts(time.Now())}); e != nil {
		t.Fatal(e)
	}
	var state string
	var attempts int
	if e = f.pool.QueryRow(t.Context(), `SELECT state,attempts FROM support_outbox WHERE ticket_id=$1`, ticket).Scan(&state, &attempts); e != nil || state != "queued" || attempts != 2 {
		t.Fatalf("stale lease changed retry: %s %d %v", state, attempts, e)
	}
	if _, e = f.pool.Exec(t.Context(), `UPDATE support_outbox SET attempts=10,available_at=now() WHERE ticket_id=$1`, ticket); e != nil {
		t.Fatal(e)
	}
	if e = f.s.dispatch(context.Background()); e != nil {
		t.Fatal(e)
	}
	if sent != 1 {
		t.Fatal("exhausted delivery sent again")
	}
}
