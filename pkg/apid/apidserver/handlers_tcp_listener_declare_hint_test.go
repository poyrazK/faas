package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestTCPListenerUndeclaredPortSaysHowToDeclareIt covers H10-49: tcp add for a
// port missing from the manifest said only that it must match a declared
// listener, with no way to declare one from the error.
func TestTCPListenerUndeclaredPortSaysHowToDeclareIt(t *testing.T) {
	h, key, store, account := buildTestServer(t)
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "tcp-undeclared", RAMMB: 256, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/tcp-undeclared/tcp-listeners", strings.NewReader(`{"name":"tcp-7000","guest_port":7000}`))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	for _, want := range []string{"Compose project's ports", "tcp-<port>", "PATCH /v1/apps/tcp-undeclared"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q missing %q", problem.Detail, want)
		}
	}
}
