// adr: 650 — admission diagnostics preserve bounded, safe metadata.
package main_test

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func admissionDiagnosticFixture(t *testing.T, id int, admission string) string {
	t.Helper()
	line := dataAPIDiagnosticFixture(t, id, "rest", "completed", 200, 1200, nil)
	return strings.TrimSuffix(line, "}") + ",\"admission\":" + admission + "}"
}

func TestDataAPIAdmissionDiagnostics(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	for i, outcome := range []string{"admitted", "queue_full", "queue_timeout", "cancelled"} {
		line := admissionDiagnosticFixture(t, i+1, fmt.Sprintf(`{"outcome":%q,"wait_ms":%d,"active":2,"queued":8,"secret":"private-sentinel"}`, outcome, i*400))
		if outcome == "queue_full" || outcome == "queue_timeout" {
			var fields map[string]any
			if err := json.Unmarshal([]byte(line), &fields); err != nil {
				t.Fatal(err)
			}
			fields["code"], fields["outcome"], fields["status"] = "data_api_overloaded", "overloaded", 503
			encoded, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			line = string(encoded)
		}
		c.Observe(line)
		c.Observe(line) // Duplicate requests cannot inflate capacity observations.
	}
	c.Observe(dataAPIDiagnosticFixture(t, 5, "rest", "completed", 200, 1, nil))
	c.Finish()
	s := (*c.Report()).Admission
	if (*c.Report()).Partial || s.Observed != 4 || s.Admitted != 1 || s.QueueFull != 1 || s.QueueTimeout != 1 || s.Cancelled != 1 || s.PeakActive != 2 || s.PeakQueued != 8 || (*c.Report()).Failures.Overloaded != 2 || (*c.Report()).Duplicates != 4 {
		t.Fatalf("wrong admission summary: %+v; failures=%+v; partial=%t", s, (*c.Report()).Failures, (*c.Report()).Partial)
	}
	if s.WaitMS.P50 == nil || *s.WaitMS.P50 != 400 || s.WaitMS.P95 == nil || *s.WaitMS.P95 != 1200 {
		t.Fatalf("wrong wait percentiles: %+v", s.WaitMS)
	}
	encoded, err := json.Marshal((*c.Report()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("unrecognized nested fields leaked")
	}
}

func TestDataAPIAdmissionDiagnosticsRejectMalformedMetadata(t *testing.T) {
	for _, admission := range []string{
		`{}`, `{"outcome":"private-sentinel","wait_ms":0,"active":1,"queued":0}`,
		`{"outcome":"admitted","active":1,"queued":0}`,
		`{"outcome":"admitted","wait_ms":-1,"active":1,"queued":0}`,
		`{"outcome":"admitted","wait_ms":0,"active":3,"queued":0}`,
		`{"outcome":"admitted","wait_ms":0,"active":1,"queued":9}`,
		`{"outcome":"admitted","wait_ms":0,"active":null,"queued":0}`,
		`{"outcome":"admitted","wait_ms":0,"active":1,"queued":-1}`,
		`{"outcome":"admitted","wait_ms":0,"active":1.5,"queued":0}`,
	} {
		t.Run(admission, func(t *testing.T) {
			c := main.NewDataAPIDiagnosticsCollector("", "")
			c.Observe(admissionDiagnosticFixture(t, 1, admission))
			if !(*c.Report()).Partial || (*c.Report()).Invalid != 1 || (*c.Report()).Summary.Requests != 0 {
				t.Fatal("invalid admission record was accepted")
			}
		})
	}
	c := main.NewDataAPIDiagnosticsCollector("", "")
	c.Observe(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil))
	c.Finish()
	if (*c.Report()).Partial || (*c.Report()).Admission.Observed != 0 || (*c.Report()).Admission.WaitMS.P50 != nil {
		t.Fatal("older runtime logs must retain explicitly empty admission coverage")
	}
}
