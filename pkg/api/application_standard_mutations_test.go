package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationStandardMutationStrictRequests(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name, valid string
		newRequest  func() any
		invalid     []string
	}{
		{"local", `{"expected_revision":1,"settings":{"require_signed":false},"additional_log_destinations":[]}`, func() any { return new(SetApplicationStandardLocalIntentRequest) }, []string{
			`{"expected_revision":1,"settings":{}}`, `{"expected_revision":1,"settings":null,"additional_log_destinations":[]}`, `{"expected_revision":1,"settings":{},"additional_log_destinations":null}`, `{"expected_revision":1,"settings":{"require_signed":null},"additional_log_destinations":[]}`, `{"expected_revision":1,"settings":{"unknown":true},"additional_log_destinations":[]}`, `{"expected_revision":1,"settings":{"require_signed":true,"require_signed":false},"additional_log_destinations":[]}`, `{"expected_revision":1,"settings":{},"additional_log_destinations":["bad"]}`,
		}},
		{"approval", `{"expected_revision":1,"standard_id":"` + id + `","version":1,"field":"require_signed","value":false,"reason":"Maintenance","expires_at":"2026-10-05T12:00:00Z"}`, func() any { return new(ApproveApplicationStandardExceptionRequest) }, []string{
			`{"expected_revision":1}`, `{"expected_revision":1,"standard_id":"` + id + `","version":1,"field":"require_signed","value":null,"reason":"Maintenance","expires_at":"2026-10-05T12:00:00Z"}`,
		}},
		{"revocation", `{"expected_revision":1}`, func() any { return new(RevokeApplicationStandardExceptionRequest) }, []string{`{}`, `null`, `{"expected_revision":null}`, `{"expected_revision":0}`, `{"expected_revision":9007199254740991}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.valid), tc.newRequest()); err != nil {
				t.Fatalf("valid request: %v", err)
			}
			invalid := append(tc.invalid, strings.Replace(tc.valid, `"expected_revision":1`, `"expected_revision":1,"expected_revision":2`, 1), strings.Replace(tc.valid, `"expected_revision":1`, `"expected_revision":1,"private":true`, 1), tc.valid+` {}`)
			for _, raw := range invalid {
				if err := json.Unmarshal([]byte(raw), tc.newRequest()); err == nil {
					t.Fatalf("accepted ambiguous or incomplete request %s", raw)
				}
			}
		})
	}
	var local SetApplicationStandardLocalIntentRequest
	if err := json.Unmarshal([]byte(`{"expected_revision":1,"settings":{},"additional_log_destinations":[]}`), &local); err != nil || string(local.Settings) != `{}` || local.AdditionalLogDestinations == nil {
		t.Fatalf("explicit clear lost: %+v %v", local, err)
	}
	var approval ApproveApplicationStandardExceptionRequest
	if err := json.Unmarshal([]byte(`{"expected_revision":1,"standard_id":"`+id+`","version":1,"field":"require_signed","value":false,"reason":"Maintenance","expires_at":"2026-10-05T12:00:00Z"}`), &approval); err != nil || string(approval.Value) != `false` {
		t.Fatalf("explicit false lost: %+v %v", approval, err)
	}
}
