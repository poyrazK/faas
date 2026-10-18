package apidserver_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlEmailUnsubscribeRouting(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	store := state.NewMemStore()
	s := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	paths := []string{"/v1/product-control/email/unsubscribe", "/v1/product-control/email-unsubscribe.js", "/v1/product-control/customer/exports/email-opt-outs", "/v1/apps/exports/product/email-opt-outs", "/dashboard/apps/exports/product-control/email-opt-outs"}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest(method, "https://example.test"+path, nil))
			if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(method, path, w.Code)
			}
		}
	}
	k := productemail.Key{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), AuthAppID: uuid.NewString(), OrganizationID: uuid.NewString(), ActorID: uuid.NewString()}
	repo, now := store.ProductEmailRepository(), time.Now().UTC()
	sub, err := repo.Put(t.Context(), productemail.Subscription{Key: k, AppSlug: "exports", AuthSlug: "auth", Work: true, Grant: []byte("encrypted"), ExpiresAt: now.Add(time.Hour)}, 0, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Enqueue(t.Context(), sub, []api.ProductNotification{{ID: "work/" + uuid.NewString(), Kind: "work_succeeded", OccurredAt: now, Revision: strings.Repeat("a", 64)}}, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(t.Context(), now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	token, err := productemail.NewUnsubscribeToken()
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := productemail.UnsubscribeHash(token)
	if _, err = repo.PrepareUnsubscribe(t.Context(), jobs[0], hash, []byte("encrypted-link"), now); err != nil {
		t.Fatal(err)
	}
	s.WithProductControlEnabled(true)
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, "https://example.test"+paths[0]+"?token="+token, nil))
		if w.Code != 200 || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://example.test"+paths[0]+"?token="+token, strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"current":true`) {
		t.Fatal("mailbox POST required session, origin or encryption", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://example.test"+paths[1], nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
