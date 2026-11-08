package objectstorage

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicReadNestedMountsIgnoreCreationOrder(t *testing.T) {
	for _, order := range [][]string{{"/assets", "/assets/images"}, {"/assets/images", "/assets"}} {
		t.Run(order[0], func(t *testing.T) {
			f := newUploadFixture(t)
			backend, err := f.registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			for _, mount := range order {
				b, err := f.store.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: f.account.ID, AppID: f.app.ID, Name: uuid.NewString(), Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: uuid.NewString(), PublicRead: true, ServeAt: mount}, 10)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.ClaimObjectBucket(t.Context(), b.AccountID, b.AppID, b.ID, "ready", "provisioning"); err != nil {
					t.Fatal(err)
				}
				if err = f.store.FinishObjectBucket(t.Context(), b.ID, "ready", "ready"); err != nil {
					t.Fatal(err)
				}
			}
			handler, err := NewPublicReadHandler(PublicReadConfig{Store: f.store, Registry: f.registry, AppsDomain: "apps.test", Next: http.NotFoundHandler()})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ path, mount, key string }{
				{"/assets/images/logo.png", "/assets/images", "logo.png"},
				{"/assets/images", "/assets/images", ""},
				{"/assets/images2/logo.png", "/assets", "images2/logo.png"},
				{"/assets/app.js", "/assets", "app.js"},
			} {
				r := httptest.NewRequest(http.MethodGet, "https://upload-test.apps.test"+tc.path, nil)
				b, key, matched, err := handler.(*publicReadHandler).lookup(r)
				if err != nil || !matched || b.ServeAt != tc.mount || key != tc.key {
					t.Fatalf("path %s routed to %s key=%q matched=%v error=%v", tc.path, b.ServeAt, key, matched, err)
				}
			}
		})
	}
}
