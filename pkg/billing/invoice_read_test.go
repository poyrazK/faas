package billing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpjson"
)

// adr:383
func TestInvoiceRefreshReaderBoundsAndCancellation(t *testing.T) {
	redirected := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/large":
			_, _ = w.Write([]byte(`"` + strings.Repeat("x", api.MaxInvoiceProviderResponseBytes) + `"`))
		case "/redirect":
			http.Redirect(w, req, "/credentials", http.StatusFound)
		case "/credentials":
			redirected++
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	r := &InvoiceFactsReader{BaseURL: srv.URL, Key: "secret", Provider: "paddle", Client: srv.Client()}
	if _, err := r.Get(t.Context(), "/large"); !errors.Is(err, httpjson.ErrResponseTooLarge) {
		t.Fatalf("large response error=%v", err)
	}
	if _, err := r.Get(t.Context(), "/redirect"); err == nil || redirected != 0 {
		t.Fatal("reader followed a credential-bearing redirect")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Get(ctx, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error=%v", err)
	}
	r.requests = 0
	for n := 0; n < api.MaxInvoiceRefreshRequests; n++ {
		if _, err := r.Get(t.Context(), "/"); err != nil {
			t.Fatal(err)
		}
	}
	var limit *InvoiceRefreshLimitError
	if _, err := r.Get(t.Context(), "/"); !errors.As(err, &limit) {
		t.Fatalf("request limit error=%v", err)
	}
}
