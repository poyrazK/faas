package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func discoverOnlyHandler(t *testing.T, failures int32, status int) (http.HandlerFunc, *atomic.Int32) {
	var discovers atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "server/discover":
			if discovers.Add(1) <= failures {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				return
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, request.ID, ProtocolVersion)
		case "tools/list":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"t","inputSchema":{"type":"object"}}]}}`, request.ID)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &discovers
}

func TestDoctorRetriesTransientGatewayDiscoveryFailure(t *testing.T) {
	old := doctorGatewayRetryDelay
	doctorGatewayRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorGatewayRetryDelay = old })
	h, discovers := discoverOnlyHandler(t, 1, http.StatusBadGateway)
	report := Doctor(context.Background(), testClient(t, h), false, "", nil)
	if !report.OK || discovers.Load() != 2 {
		t.Fatalf("report ok=%v after %d discovers: %+v", report.OK, discovers.Load(), report.Checks)
	}
}

func TestDoctorDoesNotRetryClientErrors(t *testing.T) {
	old := doctorGatewayRetryDelay
	doctorGatewayRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorGatewayRetryDelay = old })
	h, discovers := discoverOnlyHandler(t, 10, http.StatusUnauthorized)
	report := Doctor(context.Background(), testClient(t, h), false, "", nil)
	if report.OK || discovers.Load() != 1 {
		t.Fatalf("report ok=%v after %d discovers", report.OK, discovers.Load())
	}
}

func TestDoctorGivesUpAfterBoundedGatewayRetries(t *testing.T) {
	old := doctorGatewayRetryDelay
	doctorGatewayRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorGatewayRetryDelay = old })
	h, discovers := discoverOnlyHandler(t, 10, http.StatusBadGateway)
	report := Doctor(context.Background(), testClient(t, h), false, "", nil)
	if report.OK || discovers.Load() != 1+doctorGatewayRetries {
		t.Fatalf("report ok=%v after %d discovers", report.OK, discovers.Load())
	}
	if report.Discovery.DurationMS < 0 {
		t.Fatalf("negative duration %d", report.Discovery.DurationMS)
	}
}

// A valid token can be refused once while the server first fetches its
// provider's signing keys (prod 2026-11-22: 2 of 4 external-OAuth deploys
// failed verification on one 401). A probe with a token retries a bounded
// number of 401s; it still fails when they persist.
func TestDiscoveryRetriesUnauthorizedOnlyWithAToken(t *testing.T) {
	old := doctorAuthRetryDelay
	doctorAuthRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorAuthRetryDelay = old })

	h, discovers := discoverOnlyHandler(t, 2, http.StatusUnauthorized)
	c := testClient(t, h)
	c.Token = "valid-token"
	if _, _, err := discoverCatalogRetrying(context.Background(), c); err != nil || discovers.Load() != 3 {
		t.Fatalf("err=%v after %d discovers, want success on the third", err, discovers.Load())
	}

	h, discovers = discoverOnlyHandler(t, 100, http.StatusUnauthorized)
	c = testClient(t, h)
	c.Token = "revoked-token"
	if _, _, err := discoverCatalogRetrying(context.Background(), c); err == nil || discovers.Load() != 1+doctorAuthRetries {
		t.Fatalf("err=%v after %d discovers, want failure after %d", err, discovers.Load(), 1+doctorAuthRetries)
	}

	h, discovers = discoverOnlyHandler(t, 100, http.StatusUnauthorized)
	if _, _, err := discoverCatalogRetrying(context.Background(), testClient(t, h)); err == nil || discovers.Load() != 1 {
		t.Fatalf("tokenless probe retried a 401: err=%v after %d discovers", err, discovers.Load())
	}
}
