package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseAppStoragePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "storage-app/storage", want: "storage-app", ok: true},
		{path: "storage-app/storage/", want: "storage-app", ok: true},
		{path: "storage-app", ok: false},
		{path: "storage-app/storage/objects", ok: false},
		{path: "/storage", ok: false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := parseAppStoragePath(tc.path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseAppStoragePath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDashboardHandler_StorageFixture(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "storage-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	bucket, err := store.ReserveObjectBucket(t.Context(), state.ObjectBucket{
		ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID,
		Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: "external", BackendFingerprint: "fp", PhysicalName: "gregale-assets",
	}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatalf("ReserveObjectBucket: %v", err)
	}
	if _, err := store.ClaimObjectBucket(t.Context(), acct.ID, app.ID, bucket.ID, "seed-token", "provisioning"); err != nil {
		t.Fatalf("ClaimObjectBucket: %v", err)
	}
	if err := store.FinishObjectBucket(t.Context(), bucket.ID, "seed-token", "ready"); err != nil {
		t.Fatalf("FinishObjectBucket: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/storage-app/storage", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("storage code = %d\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Storage for", "assets", "ready", "Create a bucket", "Generate a signed URL", "Storage usage"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("storage body missing %q\n%s", want, rec.Body.String())
		}
	}
	if csrf := rec.Result().Cookies(); len(csrf) == 0 {
		t.Fatalf("storage response did not issue a named csrf cookie")
	} else {
		found := false
		for _, c := range csrf {
			if c.Name == dashboardStorageCSRFCookie && c.Value != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("storage response missing %s cookie: %v", dashboardStorageCSRFCookie, csrf)
		}
	}
}

func TestDashboardStorageCreateRequiresNamedCSRF(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "storage-csrf", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := dashboardPOST(t, h, cookie, "/dashboard/apps/storage-csrf/storage/buckets", map[string]string{"name": "assets"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("csrf status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid CSRF token") {
		t.Fatalf("csrf body = %s", rec.Body.String())
	}
}
