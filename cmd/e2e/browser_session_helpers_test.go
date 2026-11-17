package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// startAPIDWithSessions starts apid with a session key the test shares, so it
// can sign dashboard session cookies for browser-only actions (organization
// deletion and ownership transfer refuse API keys).
func startAPIDWithSessions(t *testing.T, pool *pgxpool.Pool) (*e2etest.Harness, *session.Manager) {
	t.Helper()
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	mgr, err := session.NewManager(keyBytes, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{"FAAS_SESSION_KEY=" + hex.EncodeToString(keyBytes)})
	return h, mgr
}

// browserSessionCookie signs in accountID as a fresh dashboard session.
func browserSessionCookie(t *testing.T, pool *pgxpool.Pool, mgr *session.Manager, accountID string) *http.Cookie {
	t.Helper()
	cookie, _ := issueSessionForTest(t, state.NewPgStore(pool), mgr, accountID, "192.0.2.30", "e2e-browser")
	return cookie
}

// doSessionReq is doReq for a signed-in browser: the session cookie instead of
// an API key, sent as a same-origin fetch.
func doSessionReq(t *testing.T, h *e2etest.Harness, cookie *http.Cookie, method, path string, body any, extraHeaders ...map[string]string) ([]byte, int) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.APIDURL+path, r)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, extra := range extraHeaders {
		for k, v := range extra {
			req.Header.Set(k, v)
		}
	}
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode
}
