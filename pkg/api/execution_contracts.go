package api

import "fmt"

// ExecutionResultValidation is host-computed. It never changes whether code
// executed successfully; clients and workflow dependents must inspect it too.
type ExecutionResultValidation struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (v *ExecutionResultValidation) Validate() error {
	if v == nil {
		return nil
	}
	if v.Status == "valid" && v.Message == "" {
		return nil
	}
	if v.Status == "invalid" && v.Message == "Run result does not match result_schema" {
		return nil
	}
	return fmt.Errorf("invalid Run result validation")
}

func (r ExecutionResponse) ContractFailed() bool {
	return r.ResultValidation != nil && r.ResultValidation.Status == "invalid"
}

func CloneExecutionResultValidation(v *ExecutionResultValidation) *ExecutionResultValidation {
	if v == nil {
		return nil
	}
	cloned := *v
	return &cloned
}
