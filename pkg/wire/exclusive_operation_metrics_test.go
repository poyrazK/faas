package wire_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestExclusiveOperationMetricsUseClosedLabels(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	ops.ObserveExclusiveOperationAdmission("broker", "accepted")
	ops.ObserveExclusiveOperationAdmission("customer-42", "arbitrary")
	ops.ObserveExclusiveOperationDispatch("lost_owner")
	ops.ObserveExclusiveOperationLeaseRenewal("renewed")
	ops.SetExclusiveOperationDueCandidates(7)

	response := httptest.NewRecorder()
	ops.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`schedd_exclusive_operation_admissions_total{outcome="accepted",source="broker"} 1`,
		`schedd_exclusive_operation_admissions_total{outcome="error",source="other"} 1`,
		`schedd_exclusive_operation_dispatch_total{outcome="lost_owner"} 1`,
		`schedd_exclusive_operation_lease_renewals_total{outcome="renewed"} 1`,
		`schedd_exclusive_operation_due_candidates 7`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing metric %q in scrape:\n%s", want, text)
		}
	}
	if strings.Contains(text, "customer-42") {
		t.Fatal("exclusive-operation metrics exposed an unbounded customer label")
	}
}
