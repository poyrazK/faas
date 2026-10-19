package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplicationStandardRolloutStrictRequests(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, valid string
		target      func() any
		invalid     []string
	}{
		{"approval", `{"approval_hash":"` + hash + `"}`, func() any { return new(ApproveApplicationStandardReviewRequest) }, []string{`{}`, `null`, `{"approval_hash":null}`, `{"approval_hash":"bad"}`, `{"approval_hash":"` + strings.Repeat("A", 64) + `"}`, `{"approval_hash":"` + hash + `","approval_hash":"` + hash + `"}`}},
		{"control", `{"expected_updated_at":"2026-10-04T12:00:00.123456Z"}`, func() any { return new(ControlApplicationStandardOperationRequest) }, []string{`{}`, `null`, `{"expected_updated_at":null}`, `{"expected_updated_at":"0001-01-01T00:00:00Z"}`, `{"expected_updated_at":"2026-10-04T12:00:00.123456789Z"}`, `{"expected_updated_at":"2026-10-04T12:00:00Z","expected_updated_at":"2026-10-04T12:00:01Z"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.valid), tc.target()); err != nil {
				t.Fatal(err)
			}
			invalid := append(tc.invalid, strings.TrimSuffix(tc.valid, "}")+`,"private":true}`, tc.valid+` {}`)
			for _, raw := range invalid {
				if err := json.Unmarshal([]byte(raw), tc.target()); err == nil {
					t.Fatalf("accepted ambiguous request %s", raw)
				}
			}
		})
	}
	var control ControlApplicationStandardOperationRequest
	if err := json.Unmarshal([]byte(`{"expected_updated_at":"2026-10-04T15:00:00.123456+03:00"}`), &control); err != nil || control.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano) != "2026-10-04T12:00:00.123456Z" {
		t.Fatalf("timestamp precision changed: %+v %v", control, err)
	}
}
