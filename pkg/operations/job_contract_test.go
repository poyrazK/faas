// adr: 645
package operations

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestJobOperationContractKeepsExecutionAndRecoverySemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.OperationDefinitionSpec)
		valid  bool
	}{
		{"job", func(*api.OperationDefinitionSpec) {}, true},
		{"explicit-reconciliation", func(s *api.OperationDefinitionSpec) { s.Recovery = api.OperationRecoveryReconcile }, true},
		{"automatic-retry", func(s *api.OperationDefinitionSpec) { s.Recovery = api.OperationRecoverySafeRetry }, false},
		{"workflow", func(s *api.OperationDefinitionSpec) { s.Workflow = "export-chain" }, false},
		{"transaction", func(s *api.OperationDefinitionSpec) { s.TransactionReceipt = api.OperationTransactionPostgres }, false},
		{"put", func(s *api.OperationDefinitionSpec) { s.Method = "PUT" }, false},
		{"invalid-job", func(s *api.OperationDefinitionSpec) { s.Job = "../other-job" }, false},
		{"oversize-job", func(s *api.OperationDefinitionSpec) { s.Job = strings.Repeat("a", api.OperationNameMaxBytes+1) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			spec.Job = "export-job"
			tc.change(&spec)
			contract, err := Compile(spec, api.MustLimitsFor(api.PlanPro).Operations)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (contract.Spec.Job != "export-job" || contract.Spec.Recovery != api.OperationRecoveryReconcile) {
				t.Fatal("lost native execution or reconciliation contract")
			}
		})
	}
}
