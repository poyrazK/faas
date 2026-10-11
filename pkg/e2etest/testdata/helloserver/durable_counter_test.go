package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDurableCounterUsesSuppliedStateAndRejectsInvalidTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, body, result string
		status             int
	}{
		{"existing", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"state":{"data":{"count":12}},"payload":{"delta":1}}`, `"count":13`, http.StatusOK},
		{"fresh", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"state":{"data":null},"payload":{"delta":1}}`, `"count":1`, http.StatusOK},
		{"bad-delta", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"payload":{"delta":"invalid"}}`, "", http.StatusUnprocessableEntity},
		{"missing-delta", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"}}`, "", http.StatusUnprocessableEntity},
		{"bad-protocol", `{"protocol_version":2,"event":"invoke","entity":{"namespace":"counters"},"payload":{"delta":1}}`, "", http.StatusUnprocessableEntity},
		{"bad-state", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"state":{"data":{"count":"bad"}},"payload":{"delta":1}}`, "", http.StatusUnprocessableEntity},
		{"overflow", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"state":{"data":{"count":9223372036854775807}},"payload":{"delta":1}}`, "", http.StatusUnprocessableEntity},
		{"underflow", `{"protocol_version":1,"event":"invoke","entity":{"namespace":"counters"},"state":{"data":{"count":-9223372036854775808}},"payload":{"delta":-1}}`, "", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			serveDurableCounter(response, httptest.NewRequest(http.MethodPost, "/__gregale/entities", strings.NewReader(tc.body)))
			if response.Code != tc.status || tc.result != "" && !strings.Contains(response.Body.String(), tc.result) {
				t.Fatalf("transition status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
