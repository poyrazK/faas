package main

import (
	"context"
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

type obsReadTestTransport func(*http.Request) (*http.Response, error)

func (f obsReadTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type obsReadTestBody struct {
	err    error
	closed bool
}

func (b *obsReadTestBody) Read([]byte) (int, error) { return 0, b.err }
func (b *obsReadTestBody) Close() error             { b.closed = true; return nil }

func TestObsFetchFailureHTTPReasonsAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		status int
		reason string
	}{
		{401, "unauthorized"}, {403, "forbidden"}, {404, "endpoint_not_found"}, {429, "rate_limited"},
		{500, "server_error"}, {502, "server_error"}, {503, "server_error"}, {302, "redirect_rejected"}, {418, "http_error"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer private-fetch-token" {
					t.Error("source read changed the credential or method contract")
				}
				w.Header().Set("Location", "/private-redirect-token")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("private-provider-response"))
			}))
			defer srv.Close()
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			var dst api.ObsServingHealth
			err := fetchObsBundleSection(context.Background(), client, srv.URL, obsCredential{bearer: "private-fetch-token"}, "serving-health", &dst)
			reason, status := obsFetchFailureDetails(err)
			if err == nil || reason != tc.reason || status == nil || *status != tc.status || requests.Load() != 1 || strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("unsafe or incorrect source failure: %q reason=%s status=%v requests=%d", err, reason, status, requests.Load())
			}
		})
	}
}

func TestObsFetchFailureRuntimeBusyRequiresReviewedHeader(t *testing.T) {
	for _, tc := range []struct {
		name, section, reason string
		status                int
		codes                 []string
	}{
		{"runtime busy", "runtime-builds", "runtime_busy", 429, []string{api.CodeObsRuntimeBusy}},
		{"missing header", "runtime-builds", "rate_limited", 429, nil},
		{"unreviewed header", "runtime-builds", "rate_limited", 429, []string{"private-error-code"}},
		{"ambiguous headers", "runtime-builds", "rate_limited", 429, []string{api.CodeObsRuntimeBusy, api.CodeAPIRateLimited}},
		{"other endpoint", "serving-health", "rate_limited", 429, []string{api.CodeObsRuntimeBusy}},
		{"other status", "runtime-builds", "server_error", 503, []string{api.CodeObsRuntimeBusy}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: make(http.Header)}
			for _, code := range tc.codes {
				resp.Header.Add(api.ErrorCodeHeader, code)
			}
			err := obsFetchResponseFailure(tc.section, resp)
			reason, status := obsFetchFailureDetails(err)
			if reason != tc.reason || status == nil || *status != tc.status || strings.Contains(err.Error(), "private-error-code") {
				t.Fatalf("classification %q, status %v, error %q", reason, status, err)
			}
		})
	}
}

func TestObsFetchFailureBodyAndTransportReasons(t *testing.T) {
	for _, tc := range []struct {
		name, reason   string
		status         bool
		transportError error
		readError      error
		body           string
	}{
		{name: "transport", reason: "transport_unavailable", transportError: errors.New("private-url: private-proxy-password")},
		{name: "request deadline", reason: "timeout", transportError: context.DeadlineExceeded},
		{name: "request cancellation", reason: "cancelled", transportError: context.Canceled},
		{name: "body deadline", reason: "timeout", readError: context.DeadlineExceeded, status: true},
		{name: "body cancellation", reason: "cancelled", readError: context.Canceled, status: true},
		{name: "body read", reason: "response_read_failed", readError: errors.New("private-body-error"), status: true},
		{name: "truncated body", reason: "response_read_failed", readError: io.ErrUnexpectedEOF, status: true},
		{name: "invalid JSON", reason: "invalid_response", body: "private-invalid-response", status: true},
		{name: "wrong JSON type", reason: "invalid_response", body: `"private-string"`, status: true},
		{name: "oversize response", reason: "response_too_large", body: strings.Repeat("x", api.ObsIncidentBundleResponseMaxBytes+1), status: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failingBody := &obsReadTestBody{err: tc.readError}
			client := &http.Client{Transport: obsReadTestTransport(func(r *http.Request) (*http.Response, error) {
				if tc.transportError != nil {
					return nil, tc.transportError
				}
				var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
				if tc.readError != nil {
					body = failingBody
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
			})}
			var dst api.ObsServingHealth
			err := fetchObsBundleSection(context.Background(), client, "http://private-user:private-password@example.test/private-path", obsCredential{}, "serving-health", &dst)
			reason, status := obsFetchFailureDetails(err)
			if err == nil || reason != tc.reason || (status != nil) != tc.status || status != nil && *status != 200 || strings.Contains(err.Error(), "private") || tc.readError != nil && !failingBody.closed {
				t.Fatalf("reason=%s status=%v closed=%t err=%v", reason, status, failingBody.closed, err)
			}
			body, marshalErr := json.Marshal(obsDeploymentSourceFailure{Reason: reason, HTTPStatus: status})
			if marshalErr != nil || strings.Contains(string(body), "private") {
				t.Fatal("raw error data entered structured metadata")
			}
		})
	}
	var dst api.ObsServingHealth
	err := fetchObsBundleSection(context.Background(), &http.Client{}, "http://private-user:private-password@example.test/\nprivate-token", obsCredential{}, "serving-health", &dst)
	reason, status := obsFetchFailureDetails(err)
	if err == nil || reason != "invalid_url" || status != nil || strings.Contains(err.Error(), "private") {
		t.Fatal("invalid URL was not redacted")
	}
}
