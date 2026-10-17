package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitOpsDelaysLeaveSmokeImmediateAndRespectCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, target, body string
		queue, cancel, ok  bool
	}{
		{name: "ordinary request", target: "/", ok: true},
		{name: "request cancellation", target: "/?gitops_delay_ms=2000", cancel: true},
		{name: "request bound", target: "/?gitops_delay_ms=3001"},
		{name: "qualification smoke", target: "/_triggers/esm/probe", body: `{"id":"qualification-smoke"}`, queue: true, ok: true},
		{name: "customer cancellation", target: "/_triggers/esm/orders", body: `{"gitops_hold_ms":10000}`, queue: true, cancel: true},
		{name: "customer delay bound", target: "/_triggers/esm/orders", body: `{"gitops_hold_ms":20001}`, queue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, tc.target, strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			var ok bool
			if tc.queue {
				ok = waitGitOpsQueueDelay(response, request)
			} else {
				ok = waitGitOpsRequestDelay(response, request)
			}
			if ok != tc.ok {
				t.Fatalf("delay continued=%t, want %t", ok, tc.ok)
			}
			if !tc.cancel && !tc.ok && response.Code != http.StatusBadRequest {
				t.Fatalf("invalid delay response: %d", response.Code)
			}
		})
	}
}
