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
