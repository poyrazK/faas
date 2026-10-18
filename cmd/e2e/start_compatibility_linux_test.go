//go:build linux

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestE2E_Start_CompatibilityPreflightBlocksMutations(t *testing.T) {
	bin := buildGregale(t)
	for _, mode := range []string{"old-api", "old-gateway", "gateway-outage"} {
		t.Run(mode, func(t *testing.T) {
			var controlCalls atomic.Int32
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				controlCalls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/internal/deployment-response-check-capabilities" || r.Header.Get("Authorization") != "" {
					t.Error("compatibility read changed its control target or sent credentials")
				}
				status := http.StatusNotFound
				if mode == "gateway-outage" {
					status = http.StatusServiceUnavailable
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(control.Close)
			var extraEnv []string
			if mode != "old-api" {
				extraEnv = append(extraEnv, "FAAS_GATEWAYD_CONTROL_URL="+control.URL)
			}
			fixture := newStartAcceptanceFixture(t, bin, "start-compat-"+mode, false, "", extraEnv...)
			origin, err := url.Parse(fixture.platform.h.APIDURL)
			if err != nil {
				t.Fatal(err)
			}
			forward := httputil.NewSingleHostReverseProxy(origin)
			var writes atomic.Int32
			adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				if mode == "old-api" && r.URL.Path == "/v1/deployments/response-check/capabilities" {
					http.NotFound(w, r)
					return
				}
				forward.ServeHTTP(w, r)
			}))
			t.Cleanup(adapter.Close)
			for i, value := range fixture.cliEnv {
				if strings.HasPrefix(value, "FAAS_API=") {
					fixture.cliEnv[i] = "FAAS_API=" + adapter.URL
				}
			}
			session := fixture.start(t)
			session.answer("Choose [1]: ", "1")
			session.wait(1)
			want := "updates"
			if mode == "gateway-outage" {
				want = "temporarily unavailable"
			}
			if !strings.Contains(session.transcript(), want) || writes.Load() != 0 {
				t.Fatalf("preflight submitted a mutation or omitted guidance: writes=%d\n%s", writes.Load(), session.transcript())
			}
			deployments, err := fixture.platform.store.ListDeploymentsForApp(t.Context(), fixture.platform.app.ID, 10, 0)
			if err != nil || len(deployments) != 0 || mode != "old-api" && controlCalls.Load() == 0 {
				t.Fatalf("compatibility failure submitted work or skipped gateway probe: deployments=%d control=%d err=%v", len(deployments), controlCalls.Load(), err)
			}
		})
	}
}
