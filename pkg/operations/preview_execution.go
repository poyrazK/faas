package operations

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ExecutionHTTP     = "http"
	ExecutionWorkflow = "workflow"
	ExecutionJob      = "job"
)

// DefinitionExecutionKind derives admission from the immutable contract,
// never from caller headers or request input. Ambiguous adapters fail closed.
// Transaction receipts are an HTTP recovery contract, not a native family.
func DefinitionExecutionKind(spec api.OperationDefinitionSpec) string {
	adapters := 0
	kind := ExecutionHTTP
	if spec.Job != "" {
		adapters++
		kind = ExecutionJob
	}
	if spec.Workflow != "" {
		adapters++
		kind = ExecutionWorkflow
	}
	if spec.TransactionReceipt != "" {
		adapters++
	}
	if adapters > 1 {
		return ""
	}
	return kind
}

func validExecutionKind(kind string) bool {
	return kind == ExecutionHTTP || kind == ExecutionWorkflow || kind == ExecutionJob
}

func (c PreviewCohort) executionKinds() []string {
	if c.ExecutionKinds == nil {
		return []string{ExecutionHTTP}
	}
	return append([]string(nil), c.ExecutionKinds...)
}

func (c PreviewCohort) validateExecutionKinds() error {
	if c.ExecutionKinds == nil {
		return nil
	}
	if len(c.ExecutionKinds) == 0 {
		return fmt.Errorf("preview execution kinds must be omitted or a nonempty allowlist")
	}
	seen := map[string]bool{}
	for _, kind := range c.ExecutionKinds {
		if !validExecutionKind(kind) || seen[kind] {
			return fmt.Errorf("preview execution kinds must be unique http, workflow or job values")
		}
		seen[kind] = true
	}
	return nil
}
