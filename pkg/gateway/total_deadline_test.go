// adr: 375
package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func setTotalBudget(h *Handler, app App, ms int) {
	h.WithEdgeRules(uploadBudgetMatcher{rule: &EdgeRuleBudgetResolved{
		ID: "total", AccountID: app.AccountID, AppID: app.ID, BudgetMs: 1000, TotalDeadlineMs: ms,
	}}, nil, nil)
}

func assertTotalTimeout(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), api.CodeRequestBudgetExceeded) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTotalDeadlineClosesUploadAndRemovesSpoolBeforeWake(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	h, backend, _ := newTestHandler(t)
	setTotalBudget(h, backend.app, 100)
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = writer.Write(bytes.Repeat([]byte("x"), int(requestBodyMemoryThreshold)+1))
	}()
	r := httptest.NewRequest(http.MethodPost, "http://"+backend.host+"/invoke", reader)
	r.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	assertTotalTimeout(t, rec)
	<-done
	if backend.admits != 0 {
		t.Fatal("expired upload reached VM admission")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload spool leaked: %v %v", entries, err)
	}
}

func TestTotalDeadlineBoundsColdWake(t *testing.T) {
	base := &fakeBackend{app: App{ID: "cold", AccountID: "owner", Plan: api.PlanPro, Type: AppTypeFunction}, host: "cold.example.test"}
	backend := &delayedAdmitBackend{fakeBackend: base, delay: 150 * time.Millisecond}
	h := NewHandlerWith(backend, NewMetrics(), nil)
	setTotalBudget(h, base.app, 50)
	r := httptest.NewRequest(http.MethodGet, "http://"+base.host+"/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	assertTotalTimeout(t, rec)
}

func TestTotalDeadlineIncludesTrustedIngressElapsedAndOverrideCannotExtend(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	setTotalBudget(h, backend.app, 50)
	r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
	r.Header.Set(trafficStartedHeader, strconv.FormatInt(time.Now().Add(-100*time.Millisecond).UnixNano(), 10))
	r.Header.Set(api.RequestBudgetDefaultOverrideHeader, "30000")
	rec := httptest.NewRecorder()
	TrustedTrafficIngress(h).ServeHTTP(rec, r)
	assertTotalTimeout(t, rec)
}

func TestPublicProxyReplacesIngressClaimsOnHTTPAndH2(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(strconv.FormatBool(h2), func(t *testing.T) {
			var accepted time.Time
			server := httptest.NewUnstartedServer(TrustedTrafficIngress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(trafficStartedHeader) != "" {
					t.Error("transport timestamp leaked into customer policy")
				}
				accepted, _ = StartTimeFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})))
			if h2 {
				server.Config.Protocols = new(http.Protocols)
				server.Config.Protocols.SetHTTP1(true)
				server.Config.Protocols.SetUnencryptedHTTP2(true)
			}
			server.Start()
			defer server.Close()
			proxy := NewInternalReverseProxy(&stubDialer{server: server}, &url.URL{Scheme: "http", Host: "internal"}, nil, h2)
			r := httptest.NewRequest(http.MethodGet, "http://app.example.test/", nil)
			r.Header.Set(trafficStartedHeader, "1")
			before := time.Now()
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, r)
			if rec.Code != http.StatusNoContent || accepted.Before(before) || accepted.After(time.Now()) {
				t.Fatalf("untrusted ingress claim survived proxy: %s status=%d", accepted, rec.Code)
			}
		})
	}
}

func TestExecutionStampKeepsTotalRuleAndCancelsBothTimers(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	setTotalBudget(h, backend.app, 250)
	r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
	r = r.WithContext(WithStartTime(r.Context(), time.Now()))
	if h.applyTotalDeadline(httptest.NewRecorder(), r, backend.app) {
		t.Fatal("fresh budget expired")
	}
	totalCtx := r.Context()
	deadline, _ := totalCtx.Deadline()
	// A policy update while wake is in progress must not replace this rule.
	setTotalBudget(h, backend.app, 1000)
	r.Header.Set(api.RequestBudgetDefaultOverrideHeader, "30000")
	h.applyEdgeRuleBudget(httptest.NewRecorder(), r, backend.app)
	if executionDeadline, _ := r.Context().Deadline(); !executionDeadline.Equal(deadline) {
		t.Fatal("execution stamp extended total deadline")
	}
	cancelStampedRequestBudget(r.Context())
	if totalCtx.Err() != context.Canceled || r.Context().Err() != context.Canceled {
		t.Fatal("a budget timer was not cancelled on handler return")
	}
}

func TestTotalDeadlineRejectsClientStreamControls(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	setTotalBudget(h, backend.app, 250)
	forwarded := false
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded = true
			if isLongLivedForward(r) {
				t.Fatal("public stream controls escaped the platform decision")
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-protocol", "grpc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if !forwarded || rec.Code != http.StatusNoContent {
		t.Fatalf("forwarded=%v status=%d; want ordinary successful exchange", forwarded, rec.Code)
	}
}
