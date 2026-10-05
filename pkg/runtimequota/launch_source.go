package runtimequota

// adr: 633
import "strings"

// LaunchSource retains the spelling actually sent to the native runtime owner.
// The full original physical permit survives app/instance row collection and
// subsequent serving resumes. Equivalent UUID spellings are not interchangeable
// native lookup keys.
type LaunchSource struct {
	Permit      CapacityPermit
	InstanceKey string
}

func (s LaunchSource) Validate() error {
	if s.Permit.Validate() != nil {
		return ErrInvalid
	}
	if s.InstanceKey != s.Permit.InstanceID && s.InstanceKey != strings.ReplaceAll(s.Permit.InstanceID, "-", "") {
		return ErrBinding
	}
	return nil
}
