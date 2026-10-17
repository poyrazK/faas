package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestInternalMailSuspensionClientRejectsPublicOriginsAndRedirects(t *testing.T) {
	token := strings.Repeat("s", 32)
	for _, origin := range []string{"https://127.0.0.1:9101", "http://localhost:9101", "http://192.0.2.1:9101", "http://127.0.0.1:9101/path", "http://user@127.0.0.1:9101", "http://127.0.0.1:9101?", "http://127.0.0.1:0"} {
		if _, err := api.NewInternalMailSuspensionClient(origin, token); err == nil {
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
	c, err := api.NewInternalMailSuspensionClient(redirect.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Probe(t.Context()); err == nil || forwarded.Load() {
		t.Fatal("redirect forwarded service credential", err)
	}
}

func TestInternalMailSuspensionTokenIsBoundedAndNotInErrors(t *testing.T) {
	for _, value := range []string{"short", strings.Repeat("x", api.InternalMailSuspensionTokenMaxBytes+1), strings.Repeat("x", 32) + "\ninside", strings.Repeat("x", 32) + "\x00"} {
		path := filepath.Join(t.TempDir(), "service.token")
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := api.LoadInternalMailSuspensionToken(path); err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("invalid token accepted or exposed", err)
		}
	}
	path := filepath.Join(t.TempDir(), "service.token")
	value := strings.Repeat("x", 32)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := api.LoadInternalMailSuspensionToken(path); err != nil || got != value {
		t.Fatal(err)
	}
}
