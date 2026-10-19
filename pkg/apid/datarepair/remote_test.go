package datarepair

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

func TestManagedRepairPurgedPayloadIsTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		problem(w, core.ErrPurged)
	}))
	defer server.Close()
	client, err := NewRemote(server.URL, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	if _, err := client.Prepare(t.Context(), scope, uuid.NewString(), uuid.NewString()); !errors.Is(err, core.ErrPurged) {
		t.Fatalf("purged data became a retryable executor outage: %v", err)
	}
}

func TestManagedRepairExecutorTrust(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://user:password@example.com", "https://example.com?token=secret", "https://example.com/other", "https://example.com/#fragment"} {
		if _, err := NewRemote(origin, strings.Repeat("x", 32)); err == nil {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
	var destinationCalls int
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls++ }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer origin.Close()
	client, err := NewRemote(origin.URL, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "default"}
	if _, err = client.Get(context.Background(), scope, uuid.NewString()); err == nil || destinationCalls != 0 {
		t.Fatal("executor credentials followed a redirect")
	}
	path := filepath.Join(t.TempDir(), "bindings.json")
	good := `{"version":1,"bindings":[{"scope":{"account_id":"` + scope.AccountID + `","app_id":"` + scope.AppID + `","environment":"default"},"generation":"` + uuid.NewString() + `","url":"` + origin.URL + `","token_env":"REPAIR_TEST_TOKEN"}]}`
	if err = os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	secret := func(string) string { return strings.Repeat("x", 32) }
	bindings, err := LoadBindings(path, secret)
	if err != nil || len(bindings) != 1 || bindings[0].Scope != scope {
		t.Fatalf("binding: %v", err)
	}
	if _, err = LoadBindings(path, func(string) string { return "" }); err == nil {
		t.Fatal("empty executor credential accepted")
	}
	bad := strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1)
	if err = os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadBindings(path, secret); err == nil {
		t.Fatal("ambiguous configuration accepted")
	}
	if bindings, err = LoadBindings("", secret); err != nil || len(bindings) != 0 {
		t.Fatal("default admission is open")
	}
}
