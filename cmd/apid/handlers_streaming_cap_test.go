package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppStreamingCapRouteAwareUsesGatewaydOverride(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppForPro(t, e, "my-api")
	app, err := e.store.AppBySlug(t.Context(), "my-api")
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	streaming := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{
		StreamingEnabled:    &streaming,
		SetStreamingEnabled: true,
	}); err != nil {
		t.Fatalf("enable streaming: %v", err)
	}

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/apps/my-api/streaming-cap" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.URL.Query().Get("host") != "api.example" || r.URL.Query().Get("path") != "/events" || r.URL.Query().Get("method") != "POST" {
			t.Fatalf("query=%v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"app_id":"` + app.ID + `","override":true,"max_body_bytes_streaming":12582912}`))
	}))
	t.Cleanup(gw.Close)
	e.s.WithGatewaydControlURL(gw.URL)

	rec := e.do(t, "GET", "/v1/apps/my-api/streaming-cap?host=api.example&path=%2Fevents&method=post", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.AppStreamingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EffectiveCap != 12582912 || got.CapKind != "endpoint-rule" {
		t.Fatalf("response=%+v, want endpoint-rule override", got)
	}
}

func TestAppStreamingCapRejectsPartialRouteShape(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppForPro(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/streaming-cap?path=%2Fevents", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestAppStreamingCapRouteAwareFallsBackToPlanOnGatewaydFailure(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppForPro(t, e, "my-api")
	e.s.WithGatewaydControlURL("http://127.0.0.1:1")

	rec := e.do(t, "GET", "/v1/apps/my-api/streaming-cap?host=api.example&path=%2Fevents&method=GET", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.AppStreamingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := e.acct.Plan.MaxResponseBodyBytes()
	if got.EffectiveCap != want || got.CapKind != "plan" {
		t.Fatalf("response=%+v, want plan fallback cap=%d", got, want)
	}
}
