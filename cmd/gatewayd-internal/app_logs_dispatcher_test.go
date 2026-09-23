package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppLogsDispatcherMissingHandlerReturnsActionableProblem(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantCode string
	}{
		{name: "live", url: "/v1/apps/demo/logs", wantCode: api.CodeAppLogsUnavailable},
		{name: "archive", url: "/v1/apps/demo/logs?archive=1", wantCode: api.CodeLogArchiveUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.url, nil)
			(&appLogsDispatcher{}).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
			}
			var problem api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != test.wantCode {
				t.Errorf("code = %q, want %q", problem.Code, test.wantCode)
			}
			if problem.Hint == "" {
				t.Error("hint is empty; log failure must give a next action")
			}
			for _, internal := range []string{"handler", "wired", "gatewayd", "S3", "FAAS_LOG_ARCHIVE"} {
				if strings.Contains(recorder.Body.String(), internal) {
					t.Errorf("response leaked internal term %q: %s", internal, recorder.Body.String())
				}
			}
		})
	}
}
