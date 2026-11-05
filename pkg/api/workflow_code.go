package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Code-first workflows (ADR-20261021204307): one entry handler replays the
// run's journal and reports the operations its code reaches next.
const (
	// WorkflowCodeProtocol names the request/response envelope version.
	WorkflowCodeProtocol = "gregale.workflow.code/v1"
	// WorkflowCodeDriverStep is the reserved step row that records each call
	// to the entry handler. Operation names cannot start with '$'.
	WorkflowCodeDriverStep = "$driver"
	// WorkflowCodeOpKey is the step-input key holding an operation descriptor.
	WorkflowCodeOpKey = "gregale_code_op"
	// WorkflowCodeModeHeader marks entry-handler calls; its value is "code".
	WorkflowCodeModeHeader = "X-Faas-Workflow-Mode"
)

var (
	ErrWorkflowCodeInvalid     = errors.New("workflow: code must name exactly one of run or path and cannot have steps")
	ErrWorkflowCodeUnsupported = errors.New("workflow: code workflows do not support this option")
)

// WorkflowCodeSpec declares a code-first workflow's entry handler. Retry is
// the default policy for the workflow's steps; its attempt count also bounds
// consecutive entry-handler failures.
type WorkflowCodeSpec struct {
	Run     string             `json:"run,omitempty" yaml:"run,omitempty" toml:"run,omitempty"`
	Path    string             `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Timeout time.Duration      `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	Retry   *WorkflowRetrySpec `json:"retry,omitempty" yaml:"retry,omitempty" toml:"retry,omitempty"`
}

// UnmarshalJSON accepts duration strings ("60s", "1d") and rejects unknown
// fields, matching WorkflowStepSpec's strict decoding.
func (c *WorkflowCodeSpec) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "run", "path", "timeout", "retry":
		default:
			return fmt.Errorf("workflow code: unknown field %q", key)
		}
	}
	var wire struct {
		Run     string             `json:"run"`
		Path    string             `json:"path"`
		Timeout json.RawMessage    `json:"timeout"`
		Retry   *WorkflowRetrySpec `json:"retry"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	timeout, err := decodeWorkflowDuration(wire.Timeout, "code.timeout")
	if err != nil {
		return err
	}
	*c = WorkflowCodeSpec{Run: wire.Run, Path: wire.Path, Timeout: timeout, Retry: wire.Retry}
	return nil
}

// MarshalJSON emits the duration-string representation.
func (c WorkflowCodeSpec) MarshalJSON() ([]byte, error) {
	var timeout any
	if c.Timeout != 0 {
		timeout = c.Timeout.String()
	}
	return json.Marshal(struct {
		Run     string             `json:"run,omitempty"`
		Path    string             `json:"path,omitempty"`
		Timeout any                `json:"timeout,omitempty"`
		Retry   *WorkflowRetrySpec `json:"retry,omitempty"`
	}{c.Run, c.Path, timeout, c.Retry})
}

// UnmarshalYAML decodes through the JSON wire shape so YAML and JSON share
// duration parsing and the unknown-field check.
func (c *WorkflowCodeSpec) UnmarshalYAML(node *yaml.Node) error {
	var fields map[string]any
	if err := node.Decode(&fields); err != nil {
		return err
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return c.UnmarshalJSON(b)
}

// WorkflowCodePath is the entry handler's request path.
func WorkflowCodePath(code WorkflowCodeSpec) string {
	if path := strings.TrimSpace(code.Path); path != "" {
		return code.Path
	}
	return "/" + strings.TrimSpace(code.Run)
}

// WorkflowCodeRetry returns the effective default step policy.
func WorkflowCodeRetry(code WorkflowCodeSpec) WorkflowRetrySpec {
	if code.Retry != nil {
		retry := *code.Retry
		if retry.Backoff == "" {
			retry.Backoff = "fixed"
		}
		return retry
	}
	return WorkflowRetrySpec{MaxAttempts: WorkflowCodeStepDefaultMaxAttempts, Backoff: "exponential"}
}

// WorkflowCodeDriverMaxAttempts bounds consecutive entry-handler failures.
func WorkflowCodeDriverMaxAttempts(code WorkflowCodeSpec) int {
	if code.Retry != nil && code.Retry.MaxAttempts > WorkflowCodeDriverDefaultMaxAttempts {
		return code.Retry.MaxAttempts
	}
	return WorkflowCodeDriverDefaultMaxAttempts
}

// ValidWorkflowCodeOpName reports whether name may label a code operation:
// 1-128 bytes of [A-Za-z0-9_.:#/-] starting with a letter or digit. The
// for_each item prefix and the '$' prefix stay reserved for the engine.
func ValidWorkflowCodeOpName(name string) bool {
	if name == "" || len(name) > WorkflowCodeOpNameMaxBytes || strings.HasPrefix(name, WorkflowForEachInternalPrefix) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if alnum || (i > 0 && strings.IndexByte("_.:#/-", c) >= 0) {
			continue
		}
		return false
	}
	return true
}

func validateWorkflowRetrySpec(retry *WorkflowRetrySpec) bool {
	return retry == nil || (retry.MaxAttempts >= 1 && retry.MaxAttempts <= WorkflowRetryMaxAttempts &&
		(retry.Backoff == "" || retry.Backoff == "fixed" || retry.Backoff == "exponential"))
}

// validateWorkflowCode checks a code-first definition after the shared
// name, concurrency and trigger checks in ValidateWorkflowDAG.
func validateWorkflowCode(spec WorkflowSpec, plan Plan) error {
	code := spec.Code
	if len(spec.Steps) != 0 {
		return ErrWorkflowCodeInvalid
	}
	if spec.MaxConcurrentActions != 0 {
		return fmt.Errorf("%w: max_concurrent_actions", ErrWorkflowCodeUnsupported)
	}
	hasRun, hasPath := strings.TrimSpace(code.Run) != "", strings.TrimSpace(code.Path) != ""
	if hasRun == hasPath {
		return ErrWorkflowCodeInvalid
	}
	if hasRun && !validWorkflowRunName(code.Run) {
		return fmt.Errorf("%w in code", ErrWorkflowInvalidRun)
	}
	if hasPath && !validWorkflowPath(code.Path) {
		return fmt.Errorf("%w in code: %q", ErrWorkflowInvalidPath, code.Path)
	}
	if code.Timeout < 0 {
		return fmt.Errorf("%w in code", ErrWorkflowTimeoutInvalid)
	}
	if code.Timeout > plan.WorkflowStepMaxTimeout() {
		return fmt.Errorf("%w in code: %s > %s", ErrWorkflowTimeoutExceeded, code.Timeout, plan.WorkflowStepMaxTimeout())
	}
	if !validateWorkflowRetrySpec(code.Retry) {
		return fmt.Errorf("%w in code", ErrWorkflowRetryInvalid)
	}
	return nil
}
