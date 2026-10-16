package promql

import (
	"encoding/json"
	"testing"
)

func TestAlertRuleDefinitionPresenceAndReset(t *testing.T) {
	var rule AlertRule
	for _, tc := range []struct {
		body     string
		reported bool
		keep     bool
		duration float64
	}{
		{`{"name":"Example","duration":60,"keepFiringFor":30,"labels":{}}`, true, true, 60},
		{`{"name":"Example","duration":0,"keepFiringFor":0,"labels":{}}`, true, true, 0},
		{`{"name":"Example"}`, false, false, 0},
		{`{"duration":null,"keepFiringFor":null,"labels":null}`, false, false, 0},
	} {
		if err := json.Unmarshal([]byte(tc.body), &rule); err != nil {
			t.Fatal(err)
		}
		if rule.DurationReported != tc.reported || (rule.KeepFiringFor != nil) != tc.keep || rule.Duration != tc.duration {
			t.Fatalf("missing definition became zero or retained prior fields: %+v", rule)
		}
	}
	for _, body := range []string{`{"duration":"private"}`, `{"keepFiringFor":"private"}`} {
		if err := json.Unmarshal([]byte(body), &rule); err == nil {
			t.Fatal("invalid duration accepted")
		}
	}
}
