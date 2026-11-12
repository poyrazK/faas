package managedrealtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedRealtimeMessageValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		maxBytes  int64
		wantTitle string
		wantLimit int64
	}{
		{name: "valid text", body: `{"data_base64":"aGk="}`},
		{name: "invalid UTF-8 text", body: `{"data_base64":"//4="}`, wantTitle: "Invalid realtime text message"},
		{name: "invalid UTF-8 as binary", body: `{"data_base64":"//4=","binary":true}`},
		{name: "over endpoint limit", body: `{"data_base64":"aGVsbG8="}`, maxBytes: 4, wantTitle: "Realtime message too large", wantLimit: 4},
		{name: "at endpoint limit", body: `{"data_base64":"aGVsbG8="}`, maxBytes: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			message, problem := decodeManagedRealtimeMessage(r)
			if problem == nil {
				problem = managedRealtimeEndpointMessageLimit(state.ManagedRealtimeEndpoint{MaxMessageBytes: tt.maxBytes}, message)
			}
			if tt.wantTitle == "" {
				if problem != nil {
					t.Fatalf("problem = %+v, want none", problem)
				}
				return
			}
			if problem == nil || problem.Status != http.StatusBadRequest || problem.Title != tt.wantTitle {
				t.Fatalf("problem = %+v, want 400 %q", problem, tt.wantTitle)
			}
			if tt.wantLimit != 0 && (problem.Limit == nil || *problem.Limit != tt.wantLimit) {
				t.Fatalf("problem limit = %v, want %d", problem.Limit, tt.wantLimit)
			}
		})
	}
}

// A node that refuses the request answers 400; that is the caller's message,
// not an unreachable owner, so apid must not answer 503 "Briefly at capacity".
func TestOwnerBadRequestIsNotCapacity(t *testing.T) {
	s := &Service{Host: fakeHost{}}
	w := httptest.NewRecorder()
	s.writeManagedRealtimeOwnerError(w, httptest.NewRequest(http.MethodPost, "/", nil), "publish message", &realtime.ManagementError{StatusCode: http.StatusBadRequest})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&body); err != nil || body.Code != "realtime_invalid" {
		t.Fatalf("body = %s, want code realtime_invalid", w.Body.String())
	}
}
