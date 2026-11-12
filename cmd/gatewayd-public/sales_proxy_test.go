package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSalesRootAndCodeAPIReachControlPlane(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Source", "control-plane")
		w.WriteHeader(204)
	}))
	t.Cleanup(upstream.Close)
	h, err := newControlPlaneProxy(upstream.URL, http.NotFoundHandler(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://sales.gregale.dev/", "https://sales.gregale.dev/login", "https://sales.gregale.dev/v1/sales/me", "https://api.gregale.dev/v1/referrals/me"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 204 || w.Header().Get("X-Source") != "control-plane" {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://customer.gregale.dev/r/code", nil))
	if w.Code != 404 {
		t.Fatal("tenant referral path stolen", w.Code)
	}
}
