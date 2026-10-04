package api

import "fmt"

// ExecutionProfile selects an immutable platform-owned dependency set.
// An empty value preserves the standard-library-only execution contract.
type ExecutionProfile string

const (
	ExecutionProfileStandard     ExecutionProfile = "standard"
	ExecutionProfilePythonDataV1 ExecutionProfile = "python-data-v1"
)

func (p ExecutionProfile) Normalized() ExecutionProfile {
	if p == "" {
		return ExecutionProfileStandard
	}
	return p
}

func (p ExecutionProfile) Valid() bool {
	switch p.Normalized() {
	case ExecutionProfileStandard, ExecutionProfilePythonDataV1:
		return true
	default:
		return false
	}
}

func (p ExecutionProfile) Validate(runtime ExecutionRuntime) error {
	switch p.Normalized() {
	case ExecutionProfileStandard:
		if runtime.Valid() {
			return nil
		}
	case ExecutionProfilePythonDataV1:
		if runtime == ExecutionRuntimePython313 {
			return nil
		}
	}
	return fmt.Errorf("profile %q is unavailable for runtime %q", p, runtime)
}
