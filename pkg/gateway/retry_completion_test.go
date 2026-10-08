// adr: 570
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type retryCompletionBackend struct {
	*fakeBackend
	touched string
}

func (b *retryCompletionBackend) PickForInstance(appID, _ string) PickResult {
	return b.Pick(appID)
}

func (b *retryCompletionBackend) TouchTarget(_, instance string, _ time.Time) {
	b.touched = instance
}

// The injected bridge raises the same pre-guest stale signal as the production
// RPC client. Separate daemon tests cover that real client and publisher.
func TestPublicRetryCompletionOwner(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		enabled, stale                    bool
		firstStatus, lastStatus, attempts int
		clearProvenance                   bool
	}{
		{"replayed_success", true, true, 503, 200, 2, false},
		{"replayed_failure", true, true, 503, 503, 2, false},
		{"application_error", true, false, 500, 500, 1, false},
		{"retry_disabled", false, true, 503, 503, 1, false},
		{"clears_sibling_provenance", true, true, 503, 200, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousProvider := otel.GetTracerProvider()
			spans := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previousProvider) })
			app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, SessionAffinity: true}
			original := Target{AppID: app.ID, DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), NodeID: "first-node", Region: "first-region", ImageDigest: "first-image"}
			sibling := Target{AppID: app.ID, DeploymentID: original.DeploymentID, InstanceID: uuid.NewString(), NodeID: "last-node", Region: "last-region", ImageDigest: "last-image", WakeID: "historical-sibling-wake"}
			if tc.clearProvenance {
				sibling.Region, sibling.ImageDigest = "", ""
			}
			backend := &retryCompletionBackend{fakeBackend: &fakeBackend{app: app, host: "app.test"}}
			backend.AddTarget(original)
			backend.AddTarget(sibling)
			var logs bytes.Buffer
			h := NewHandlerWith(backend, nil, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))).
				WithRetryEnabled(tc.enabled).WithRetryDefault(RetryPolicy{MaxAttempts: 2})
			h.lastSeen = NewMemoryLastSeen()
			h.egressSink = egresssink.NewEgressSink()
			h.requestTelemetry = makeTestRecorder()
			attempts := 0
			h.WithForwarding(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts++
					status := tc.lastStatus
					cookie := "served"
					if target.InstanceID == original.InstanceID {
						status = tc.firstStatus
						cookie = "failed"
						if tc.stale {
							markStaleTarget(r.Context())
						}
					}
					http.SetCookie(w, &http.Cookie{Name: cookie, Value: "guest", Path: "/"})
					w.WriteHeader(status)
					_, _ = w.Write([]byte("response"))
				})
			})
			response := httptest.NewRecorder()
			http.SetCookie(response, &http.Cookie{Name: "platform", Value: "retained", Path: "/"})
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://app.test/work", nil))
			if response.Code != tc.lastStatus || attempts != tc.attempts {
				t.Fatalf("status=%d attempts=%d want=%d/%d body=%s", response.Code, attempts, tc.lastStatus, tc.attempts, response.Body)
			}
			owner := original
			if tc.attempts == 2 {
				owner = sibling
			}
			rows := h.requestTelemetry.DrainBatch(8)
			if len(rows) != 1 || rows[0].InstanceID != owner.InstanceID || rows[0].NodeID != owner.NodeID || rows[0].Region != owner.Region || rows[0].ImageDigest != owner.ImageDigest || rows[0].WakeID != "" || rows[0].ColdBoot || rows[0].Status != tc.lastStatus || rows[0].Count != 1 || rows[0].AppID.String() != app.ID || rows[0].AccountID.String() != app.AccountID || rows[0].DeploymentID.String() != owner.DeploymentID {
				t.Errorf("completion telemetry=%+v want owner=%+v, one warm logical request", rows, owner)
			}
			wantTouched := ""
			if tc.lastStatus == 200 {
				wantTouched = owner.InstanceID
			}
			if backend.touched != wantTouched {
				t.Errorf("successful activity=%q want=%q", backend.touched, wantTouched)
			}
			for _, target := range []Target{original, sibling} {
				_, touched := h.lastSeen.Get(target.InstanceID)
				if touched != (target.InstanceID == wantTouched) {
					t.Errorf("last seen %s touched=%v", target.NodeID, touched)
				}
			}
			records := h.egressSink.DrainRecords()
			wantBytes := uint64(0)
			if tc.lastStatus == 200 {
				wantBytes = uint64(len("response"))
			}
			if len(records) != 1 || records[0].InstanceID != owner.InstanceID || records[0].Requests != 1 || records[0].Bytes != wantBytes || records[0].ColdBoots != 0 {
				t.Errorf("usage=%+v want one request and %d bytes for %s", records, wantBytes, owner.InstanceID)
			}
			cookies := response.Result().Cookies()
			affinityRequest := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
			counts := make(map[string]int)
			for _, cookie := range cookies {
				counts[cookie.Name]++
				affinityRequest.AddCookie(cookie)
			}
			if counts["platform"] != 1 || counts[sessionAffinityCookieName] != 1 || (tc.attempts == 2 && (counts["failed"] != 0 || counts["served"] != 1)) || h.affinityTargetFromRequest(affinityRequest, app.ID) != owner.InstanceID {
				t.Errorf("completion cookies=%v affinity=%q want=%s", counts, h.affinityTargetFromRequest(affinityRequest, app.ID), owner.InstanceID)
			}
			found := false
			for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
				var fields map[string]any
				if json.Unmarshal(line, &fields) == nil && fields["msg"] == "gateway_request" {
					found = true
					image, _ := fields["image_digest"].(string)
					if fields["instance_id"] != owner.InstanceID || fields["node_id"] != owner.NodeID || image != owner.ImageDigest {
						t.Errorf("completion log=%s", line)
					}
				}
			}
			if !found {
				t.Errorf("missing completion identity log: %s", &logs)
			}
			completed := 0
			for _, span := range spans.Ended() {
				if span.Name() != "gateway.request" && span.Name() != "gateway.forward" {
					continue
				}
				completed++
				attributes := make(map[string]string)
				for _, attribute := range span.Attributes() {
					attributes[string(attribute.Key)] = attribute.Value.AsString()
				}
				if attributes["instance_id"] != owner.InstanceID || attributes["node_id"] != owner.NodeID || attributes["image_digest"] != owner.ImageDigest {
					t.Errorf("completion span %s attributes=%v", span.Name(), attributes)
				}
			}
			if completed != 2 {
				t.Errorf("completion spans=%d want=2", completed)
			}
		})
	}
}
