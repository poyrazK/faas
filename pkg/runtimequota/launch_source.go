package runtimequota

// adr: 761
import "strings"

// LaunchSource retains the spelling actually sent to the native runtime owner.
// The full original physical permit survives app/instance row collection and
// subsequent serving resumes. Equivalent UUID spellings are not interchangeable
// native lookup keys.
type LaunchSource struct {
	Permit      CapacityPermit
	InstanceKey string
	TaskID      string `json:",omitempty"` // Original durable task, never a current-row backfill.
}

func (s LaunchSource) Validate() error {
	if s.Permit.Validate() != nil {
		return ErrInvalid
	}
	if s.InstanceKey != s.Permit.InstanceID && s.InstanceKey != strings.ReplaceAll(s.Permit.InstanceID, "-", "") {
		return ErrBinding
	}
	if s.TaskID != "" && s.ValidateTask() != nil {
		return ErrBinding
	}
	return nil
}

// ValidateTask requires the relationship retained at dispatch. Historical task
// sources without this provenance remain readable, but cannot prove a task row.
// adr: 781
func (s LaunchSource) ValidateTask() error {
	instance, err := AppTaskInstanceID(s.TaskID, s.Permit.RunID)
	if err != nil || s.Permit.Kind != CapacityAppTask || s.Permit.InstanceID != instance {
		return ErrBinding
	}
	return nil
}
