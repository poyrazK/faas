package api

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ExecutionWorkflowIDMaxBytes = 96
	ExecutionStepLabelMaxBytes  = 128
)

// ValidateExecutionWorkflowMetadata keeps workflow identifiers indexable and
// step labels bounded. Workflow metadata remains in the control plane and is
// not included in the sealed guest payload.
func ValidateExecutionWorkflowMetadata(workflowID, stepLabel string) error {
	if workflowID == "" {
		if stepLabel != "" {
			return fmt.Errorf("step_label requires workflow_id")
		}
		return nil
	}
	if len(workflowID) > ExecutionWorkflowIDMaxBytes || !workflowIDChar(workflowID[0], true) {
		return fmt.Errorf("workflow_id must start with an ASCII letter or digit and contain at most %d bytes", ExecutionWorkflowIDMaxBytes)
	}
	for i := 1; i < len(workflowID); i++ {
		if !workflowIDChar(workflowID[i], false) {
			return fmt.Errorf("workflow_id may contain only ASCII letters, digits, period, underscore, colon, and hyphen")
		}
	}
	if stepLabel == "" {
		return nil
	}
	if len(stepLabel) > ExecutionStepLabelMaxBytes || !utf8.ValidString(stepLabel) || strings.TrimSpace(stepLabel) != stepLabel {
		return fmt.Errorf("step_label must be trimmed UTF-8 text of at most %d bytes", ExecutionStepLabelMaxBytes)
	}
	for _, r := range stepLabel {
		if unicode.IsControl(r) {
			return fmt.Errorf("step_label must not contain control characters")
		}
	}
	return nil
}

func workflowIDChar(c byte, first bool) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return true
	}
	return !first && (c == '.' || c == '_' || c == ':' || c == '-')
}
