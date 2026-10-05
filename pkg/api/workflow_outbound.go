package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

// WorkflowOutboundSpec invokes an existing managed integration through outboundd.
// Path is fixed and relative; input uses the normal workflow JSON templates.
type WorkflowOutboundSpec struct {
	IntegrationID        string `json:"integration_id" yaml:"integration_id" toml:"integration_id"`
	Method               string `json:"method" yaml:"method" toml:"method"`
	Path                 string `json:"path" yaml:"path" toml:"path"`
	IdempotencySupported bool   `json:"idempotency_supported,omitempty" yaml:"idempotency_supported,omitempty" toml:"idempotency_supported,omitempty"`
}

func (s *WorkflowOutboundSpec) UnmarshalJSON(data []byte) error {
	type wire WorkflowOutboundSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var out wire
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*s = WorkflowOutboundSpec(out)
	return nil
}

func (s WorkflowOutboundSpec) SafeToRepeat() bool {
	return s.Method == "GET" || s.Method == "HEAD" || s.IdempotencySupported
}

var ErrWorkflowInvalidOutbound = errors.New("workflow: outbound requires a canonical integration UUID, allowed method, fixed relative path, and no outer method; mutating retries require idempotency_supported")

func ValidateWorkflowOutboundStep(step WorkflowStepSpec) error {
	s := step.Outbound
	if s == nil {
		return nil
	}
	if len(step.Name) == 0 || len(step.Name) > WorkflowOutboundStepNameMaxBytes {
		return ErrWorkflowInvalidOutbound
	}
	for i := range len(step.Name) {
		if step.Name[i] < 0x21 || step.Name[i] > 0x7e {
			return ErrWorkflowInvalidOutbound
		}
	}
	id, err := uuid.Parse(s.IntegrationID)
	if err != nil || id == uuid.Nil || id.String() != s.IntegrationID || step.Method != "" {
		return ErrWorkflowInvalidOutbound
	}
	if !routepolicy.CanonicalPath(s.Path) || !validWorkflowMethod(s.Method) || s.Method == "OPTIONS" {
		return ErrWorkflowInvalidOutbound
	}
	if (s.Method == "GET" || s.Method == "HEAD") && len(step.Input) != 0 {
		return ErrWorkflowInvalidOutbound
	}
	if int64(len(step.Input)) > WorkflowOutboundBodyMaxBytes {
		return ErrWorkflowInvalidOutbound
	}
	if step.Retry != nil && step.Retry.MaxAttempts > 1 && !s.SafeToRepeat() {
		return ErrWorkflowInvalidOutbound
	}
	return nil
}
