package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestInternalMailQuotaWarningClientRejectsPublicOriginsAndRedirects(t *testing.T) {
	token := strings.Repeat("s", 32)
	for _, origin := range []string{"https://127.0.0.1:9101", "http://localhost:9101", "http://192.0.2.1:9101", "http://127.0.0.1:9101/path", "http://user@127.0.0.1:9101", "http://127.0.0.1:9101?", "http://127.0.0.1:0"} {
		if _, err := api.NewInternalMailQuotaWarningClient(origin, token); err == nil {
			t.Fatal("unsafe operator origin accepted", origin)
		}
	}
	var forwarded atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c, err := api.NewInternalMailQuotaWarningClient(redirect.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Probe(t.Context()); err == nil || forwarded.Load() {
		t.Fatal("redirect forwarded service credential", err)
	}
}

func TestInternalMailQuotaWarningTokenIsBoundedAndNotInErrors(t *testing.T) {
	for _, value := range []string{"short", strings.Repeat("x", api.InternalMailQuotaWarningTokenMaxBytes+1), strings.Repeat("x", 32) + "\ninside", strings.Repeat("x", 32) + "\x00"} {
		path := filepath.Join(t.TempDir(), "service.token")
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := api.LoadInternalMailQuotaWarningToken(path); err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("invalid token accepted or exposed", err)
		}
	}
	path := filepath.Join(t.TempDir(), "service.token")
	value := strings.Repeat("x", 32)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := api.LoadInternalMailQuotaWarningToken(path); err != nil || got != value {
		t.Fatal(err)
	}
}

func TestInternalMailQuotaWarningClientRejectsMalformedProofs(t *testing.T) {
	for _, reply := range []string{
		`{"followup":true}`,
		`{"followup":true,"intent_id":"invalid","lease_id":"invalid","day":"2026-10-17T00:00:00Z","observed_at":"2026-10-17T12:00:00Z","plan":"pro","used_mb_seconds":925286400,"quota_gb":250}`,
		`{"followup":false,"recipient":"attacker@example.net"}`,
		`{"followup":false} {}`,
		strings.Repeat(" ", api.InternalMailQuotaWarningCommandMaxBytes+1),
	} {
		t.Run(reply[:min(len(reply), 40)], func(t *testing.T) {
			operator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(reply))
			}))
			defer operator.Close()
			client, err := api.NewInternalMailQuotaWarningClient(operator.URL, strings.Repeat("n", 32))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Warn(t.Context(), uuid.NewString(), time.Now().UTC().Truncate(24*time.Hour), api.PlanPro); err == nil {
				t.Fatal("malformed follow-up response accepted")
			}
		})
	}
}
