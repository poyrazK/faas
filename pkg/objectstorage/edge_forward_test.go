// adr: 1343
// edge_forward_test.go pins ADR-1343 §2: a compute-node edge, which holds no
// storage credentials, sends exactly the requests the public-read and upload
// handlers would answer to the control plane, and nothing else.

package objectstorage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedForwardBucket(t *testing.T) *state.MemStore {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "edge-forward@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "assets-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: "backend", BackendFingerprint: "fingerprint", PhysicalName: "gregale-assets",
		PublicRead: true, ServeAt: "/assets",
	}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "token", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, bucket.ID, "token", "ready"); err != nil {
		t.Fatal(err)
	}
	return store
}

// adr: 1343
func TestPublicReadForwarderForwardsOnlyBucketReads(t *testing.T) {
	store := seedForwardBucket(t)
	served := func(name string, hit *string) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { *hit = name })
	}
	for _, tc := range []struct {
		name, method, host, path, want string
	}{
		{"bucket object", http.MethodGet, "assets-app.gregale.dev", "/assets/logo.png", "forward"},
		{"bucket head", http.MethodHead, "assets-app.gregale.dev", "/assets/logo.png", "forward"},
		{"bucket root", http.MethodGet, "assets-app.gregale.dev", "/assets", "forward"},
		{"app path", http.MethodGet, "assets-app.gregale.dev", "/api/users", "next"},
		{"bucket write", http.MethodPost, "assets-app.gregale.dev", "/assets/logo.png", "next"},
		{"other app", http.MethodGet, "other-app.gregale.dev", "/assets/logo.png", "next"},
		{"nested host", http.MethodGet, "x.assets-app.gregale.dev", "/assets/logo.png", "next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hit string
			h, err := NewPublicReadForwarder(PublicReadForwardConfig{
				Store: store, AppsDomain: "gregale.dev",
				Forward: served("forward", &hit), Next: served("next", &hit),
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			req.Host = tc.host
			h.ServeHTTP(httptest.NewRecorder(), req)
			if hit != tc.want {
				t.Fatalf("%s %s%s went to %q, want %q", tc.method, tc.host, tc.path, hit, tc.want)
			}
		})
	}
}

func TestNewPublicReadForwarderRequiresItsHandlers(t *testing.T) {
	if _, err := NewPublicReadForwarder(PublicReadForwardConfig{Store: state.NewMemStore(), Next: http.NotFoundHandler()}); err == nil {
		t.Fatal("forwarder without Forward was accepted")
	}
}

// adr: 1343
func TestIsUploadPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/uploads/avatars":                  true,
		"/uploads/avatars/receipts/abc-123": true,
		"/uploads/":                         false,
		"/uploads/Avatars":                  false,
		"/uploads/avatars/extra":            false,
		"/uploads/1avatars":                 false,
		"/api/uploads/avatars":              false,
		"/uploads/avatars/receipts/":        true,
	} {
		if got := IsUploadPath(path); got != want {
			t.Errorf("IsUploadPath(%q) = %v, want %v", path, got, want)
		}
	}
}
