package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const correlationID = "019a767e7f0070008000000000000001"

func TestResponseRequestIDValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
		want   string
	}{
		{"hex", http.Header{api.RequestIDHeader: {correlationID}}, correlationID},
		{"uuid", http.Header{api.RequestIDHeader: {"019a767e-7f00-7000-8000-000000000001"}}, "019a767e-7f00-7000-8000-000000000001"},
		{"upper", http.Header{"x-faas-request-id": {strings.ToUpper(correlationID)}}, strings.ToUpper(correlationID)},
		{"missing", nil, ""},
		{"empty", http.Header{api.RequestIDHeader: {""}}, ""},
		{"unknown", http.Header{api.RequestIDHeader: {"header-private"}}, ""},
		{"short", http.Header{api.RequestIDHeader: {correlationID[:31]}}, ""},
		{"not hex", http.Header{api.RequestIDHeader: {strings.Repeat("z", 32)}}, ""},
		{"unicode", http.Header{api.RequestIDHeader: {strings.Repeat("é", 16)}}, ""},
		{"spaces", http.Header{api.RequestIDHeader: {" " + correlationID}}, ""},
		{"newline", http.Header{api.RequestIDHeader: {correlationID + "\n"}}, ""},
		{"comma", http.Header{api.RequestIDHeader: {correlationID + ", " + correlationID}}, ""},
		{"duplicates", http.Header{api.RequestIDHeader: {correlationID, correlationID}}, ""},
		{"case duplicates", http.Header{api.RequestIDHeader: {correlationID}, "x-faas-request-id": {correlationID}}, ""},
		{"bad uuid", http.Header{api.RequestIDHeader: {"019a767e_7f00-7000-8000-000000000001"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := api.ResponseRequestID(tc.header); got != tc.want {
				t.Fatalf("request id=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestProblemRequestIDMetadata(t *testing.T) {
	var missing *api.Problem
	if missing.RequestID() != "" {
		t.Fatal("nil problem invented an ID")
	}
	p := api.NewProblem(503, "service_unavailable", "Unavailable", "try later")
	p.WithRequestID(correlationID)
	if p.RequestID() != correlationID {
		t.Fatal("client correlation was not retained")
	}
	p.WithRequestID("invalid")
	if p.RequestID() != "" {
		t.Fatal("invalid correlation did not clear earlier metadata")
	}
}

func TestAPIErrorRetainsRequestIDForJSONAndNonJSON(t *testing.T) {
	for _, body := range []string{`{"status":503,"code":"service_unavailable","detail":"try later","request_id":"body-private"}`, `<html>proxy-private</html>`, `{bad json`, ``} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(api.RequestIDHeader, correlationID)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := api.NewClient(server.URL, "").UsageSummary(t.Context(), "")
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type changed: %v", err)
			}
			if got := apiErr.Problem.RequestID(); got != correlationID {
				t.Fatalf("request id was lost: %q", got)
			}
			wire, err := json.Marshal(apiErr.Problem)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), `"request_id"`) || strings.Contains(string(wire), correlationID) {
				t.Fatal("client error metadata changed the API problem body")
			}
			recorder := httptest.NewRecorder()
			ownID := "00000000000000000000000000000002"
			recorder.Header().Set(api.RequestIDHeader, ownID)
			api.WriteProblem(recorder, &apiErr.Problem)
			if api.ResponseRequestID(recorder.Header()) != ownID {
				t.Fatal("forwarding an SDK problem changed the server correlation header")
			}
		})
	}
}

func TestAPIErrorRequestIDMissingOrInvalid(t *testing.T) {
	for _, values := range [][]string{nil, {"header-private"}, {correlationID, correlationID}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for _, value := range values {
				w.Header().Add(api.RequestIDHeader, value)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"not_found","request_id":"019a767e7f0070008000000000000001"}`)
		}))
		_, err := api.NewClient(server.URL, "").UsageSummary(t.Context(), "")
		server.Close()
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Problem.RequestID() != "" {
			t.Fatalf("untrusted request id retained: %v", err)
		}
	}
}

// The existing traffic-policy retry contract allows four pre-write refusals.
// Correlation must refer to the last response, without IDs from earlier sends.
func TestAPIErrorRequestIDComesFromFinalFailedAttempt(t *testing.T) {
	for _, finalID := range []string{"00000000000000000000000000000004", ""} {
		t.Run(fmt.Sprint(finalID != ""), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempt := calls.Add(1)
				id := fmt.Sprintf("%032x", attempt)
				if attempt == 4 {
					id = finalID
				}
				if id != "" {
					w.Header().Set(api.RequestIDHeader, id)
				}
				w.Header().Set("Retry-After", "0")
				problem := api.ErrTrafficPolicyTooComplex("time", "milliseconds", 2000, 2001)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(problem.Status)
				_ = json.NewEncoder(w).Encode(problem)
			}))
			defer server.Close()
			_, err := api.NewClient(server.URL, "").Rollback(t.Context(), "app")
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || calls.Load() != 4 {
				t.Fatalf("attempts=%d error=%v", calls.Load(), err)
			}
			got := apiErr.Problem.RequestID()
			if finalID == "" {
				if got != "" {
					t.Fatalf("stale id retained: %q", got)
				}
			} else if got != finalID {
				t.Fatalf("id=%q, want %s", got, finalID)
			}
		})
	}
}
