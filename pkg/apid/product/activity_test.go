package product

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlActivityService(t *testing.T) {
	s, store, acct, app, customer := overviewFixture(t)
	if _, err := s.customerActivity(t.Context(), acct, app, "", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unscoped activity: %v", err)
	}
	// Direct legacy assignment has no reviewed-change receipt. An empty history
	// must not become an invented setup or activation milestone.
	p, err := s.customerActivity(t.Context(), acct, app, customer.TenantID, "")
	if err != nil || len(p.Events) != 0 || p.Events == nil {
		t.Fatalf("legacy history: %+v %v", p, err)
	}
	for range api.ProductControlActivityPageSize + 1 {
		customer.Suspended = !customer.Suspended
		preview, err := store.PreviewProductCustomerChange(t.Context(), acct.ID, app.ID, customer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ApplyProductCustomerChange(t.Context(), acct.ID, app.ID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: preview.Hash, Customer: preview.After}); err != nil {
			t.Fatal(err)
		}
	}
	p, err = s.customerActivity(t.Context(), acct, app, customer.TenantID, "")
	if err != nil || len(p.Events) != api.ProductControlActivityPageSize || p.NextCursor == "" {
		t.Fatalf("activity page: %+v %v", p, err)
	}
	for _, e := range p.Events {
		if e.ReviewURL != productcontrol.ActivityReviewURL(app.Slug, customer.TenantID, e) {
			t.Fatal("incorrect source link")
		}
	}
	next, err := s.customerActivity(t.Context(), acct, app, customer.TenantID, p.NextCursor)
	if err != nil || len(next.Events) != 1 || next.NextCursor != "" {
		t.Fatalf("older history: %+v %v", next, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.customerActivity(ctx, acct, app, customer.TenantID, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("partial success after cancel: %v", err)
	}
}

type activityDocumentSource struct {
	state.ProductOwnerStore
	doc   api.ProductBillingDocument
	scope state.ProductBillingScope
}

func (f activityDocumentSource) GetProductOwnerDocument(_ context.Context, scope state.ProductBillingScope, id string) (api.ProductBillingDocument, error) {
	if scope != f.scope || id != f.doc.ID {
		return api.ProductBillingDocument{}, state.ErrNotFound
	}
	return f.doc, nil
}

func TestProductControlActivityDocumentSource(t *testing.T) {
	scope := state.ProductBillingScope{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), Period: "2026-09"}
	doc := api.ProductBillingDocument{ID: uuid.NewString(), TenantID: scope.TenantID, Period: scope.Period, Kind: "statement"}
	c := &ownerContext{owner: activityDocumentSource{doc: doc, scope: scope}}
	r := httptest.NewRequest("GET", "/", nil)
	// Focused links load their exact document, even when it lies beyond a normal
	// document list page. No list call or unrelated document can replace it.
	p, err := ownerDocumentPage(r, c, scope, url.Values{"document": {doc.ID}})
	if err != nil || len(p.Documents) != 1 || p.Documents[0].ID != doc.ID || p.NextCursor != "" {
		t.Fatalf("focused document: %+v %v", p, err)
	}
	if _, err = ownerDocumentPage(r, c, scope, url.Values{"document": {doc.ID}, "documents_cursor": {uuid.NewString()}}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ambiguous document selector: %v", err)
	}
	foreign := scope
	foreign.TenantID = uuid.NewString()
	if _, err = ownerDocumentPage(r, c, foreign, url.Values{"document": {doc.ID}}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign document: %v", err)
	}
}

func TestProductControlOwnerSurfaceGate(t *testing.T) {
	for _, tc := range []struct {
		path  string
		gated bool
	}{
		{"/v1/apps/exports/product/customers/activity", true},
		{"/v1/apps/exports/product", true},
		{"/dashboard/apps/exports/product-control/activity", true},
		{"/v1/product-control/customer/exports/guidance", true},
		{"/v1/product-control/portal-guidance.js", true},
		{"/v1/platform-tenant-self/product", true},
		{"/v1/product-controls/other", false},
		{"/v1/apps/exports/productivity", false},
		{"/v1/apps/exports/customer-operations", false},
		{"/dashboard/apps/exports", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := &Service{}
			called := false
			h := s.SurfaceGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("DELETE", tc.path, nil))
			if tc.gated && (called || w.Code != 404 || w.Header().Get("Cache-Control") != "no-store") || !tc.gated && (!called || w.Code != 204) {
				t.Fatalf("disabled surface gate: %d called=%v", w.Code, called)
			}
			s.Enabled = true
			called = false
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("DELETE", tc.path, nil))
			if !called || w.Code != 204 {
				t.Fatal("enabled surface was hidden")
			}
		})
	}
}
