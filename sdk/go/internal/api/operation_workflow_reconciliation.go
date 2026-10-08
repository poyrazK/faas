package api

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const OperationWorkflowReconciliationKind = "gregale.workflow-reconciliation.v1"

type OperationWorkflowReconciliation struct {
	Workflow                string `json:"workflow"`
	InstanceID              string `json:"instance_id"`
	AuthoritativeState      string `json:"authoritative_state"`
	SourceRevision          string `json:"source_revision"`
	ExpectedReportRevision  int64  `json:"expected_report_revision"`
	ContractVersion         int    `json:"contract_version"`
	ObservedState           string `json:"observed_state,omitempty"`
	ObservedReportRevision  int64  `json:"observed_report_revision"`
	ObservedContractVersion int    `json:"observed_contract_version"`
	Status                  string `json:"status"`
}
type OperationWorkflowReconciliationPayload struct {
	Kind           string                          `json:"kind"`
	Reconciliation OperationWorkflowReconciliation `json:"reconciliation"`
}

func EvaluateOperationWorkflowReconciliation(input OperationWorkflowReconciliation, snapshot *OperationWorkflowInstanceSnapshot) (OperationWorkflowReconciliation, error) {
	input.ObservedState = ""
	input.ObservedReportRevision = 0
	input.ObservedContractVersion = 0
	if snapshot != nil {
		if snapshot.Workflow != input.Workflow || snapshot.InstanceID != input.InstanceID {
			return input, fmt.Errorf("reconciliation snapshot identity differs")
		}
		input.ObservedContractVersion = snapshot.ContractVersion
		if snapshot.State != nil {
			state := snapshot.State
			if state.Workflow != input.Workflow || state.InstanceID != input.InstanceID || state.ContractVersion != snapshot.ContractVersion || state.Revision < 1 || state.Revision > 9007199254740991 {
				return input, fmt.Errorf("invalid retained reconciliation state")
			}
			input.ObservedState = state.State
			input.ObservedReportRevision = state.Revision
		}
	}
	input.Status = OperationWorkflowReconciliationStatus(input)
	return input, ValidateOperationWorkflowReconciliation(input)
}
func OperationWorkflowReconciliationStatus(input OperationWorkflowReconciliation) string {
	if input.ObservedContractVersion != 0 && input.ContractVersion != input.ObservedContractVersion {
		return "contract_version_mismatch"
	}
	if input.ObservedReportRevision == 0 {
		return "report_missing"
	}
	if input.ExpectedReportRevision > 0 && input.ObservedReportRevision > input.ExpectedReportRevision {
		return "report_ahead"
	}
	if input.ExpectedReportRevision > 0 && input.ObservedReportRevision < input.ExpectedReportRevision {
		return "report_behind"
	}
	if input.AuthoritativeState != input.ObservedState {
		return "state_mismatch"
	}
	return "in_sync"
}
func (input OperationWorkflowReconciliation) RefreshNeeded() bool {
	return input.Status == "report_missing" || input.Status == "report_behind" || input.Status == "state_mismatch"
}
func ValidateOperationWorkflowReconciliation(input OperationWorkflowReconciliation) error {
	// Reuse byte, slug, and text validation shared with application decision evidence.
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: input.Workflow, InstanceID: input.InstanceID, Code: input.AuthoritativeState, Description: "reconciliation", RuleID: "reconciliation", RuleVersion: input.SourceRevision}); err != nil {
		return err
	}
	if input.ExpectedReportRevision < 0 || input.ExpectedReportRevision > 9007199254740991 || input.ObservedReportRevision < 0 || input.ObservedReportRevision > 9007199254740991 || input.ContractVersion < 1 || input.ContractVersion > 1000000 || input.ObservedContractVersion < 0 || input.ObservedContractVersion > 1000000 {
		return fmt.Errorf("invalid reconciliation revision or contract version")
	}
	if input.ObservedReportRevision == 0 && input.ObservedState != "" || input.ObservedReportRevision > 0 && (input.ObservedState == "" || input.ObservedContractVersion == 0) {
		return fmt.Errorf("inconsistent observed reconciliation state")
	}
	if input.ObservedState != "" {
		if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "reconciliation", InstanceID: "reconciliation", Code: input.ObservedState, Description: "reconciliation", RuleID: "reconciliation", RuleVersion: "reconciliation"}); err != nil {
			return err
		}
	}
	if input.Status != OperationWorkflowReconciliationStatus(input) {
		return fmt.Errorf("reconciliation status differs from its observations")
	}
	return nil
}
func ParseOperationWorkflowReconciliation(data []byte) (*OperationWorkflowReconciliation, error) {
	if !operationMilestoneHasKind(data, OperationWorkflowReconciliationKind) {
		return nil, nil
	}
	var payload OperationWorkflowReconciliationPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if err := ValidateOperationWorkflowReconciliation(payload.Reconciliation); err != nil {
		return nil, err
	}
	return &payload.Reconciliation, nil
}
