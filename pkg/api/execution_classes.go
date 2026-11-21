package api

// ExecutionClass selects a bounded admission-to-result deadline. The empty
// value preserves the original standard class and its plan-specific defaults.
type ExecutionClass string

const (
	ExecutionClassStandard ExecutionClass = "standard"
	ExecutionClassExtended ExecutionClass = "extended"
)

func (c ExecutionClass) Normalized() ExecutionClass {
	if c == "" {
		return ExecutionClassStandard
	}
	return c
}

func (c ExecutionClass) Valid() bool {
	return c.Normalized() == ExecutionClassStandard || c == ExecutionClassExtended
}

func (c ExecutionClass) ValidTimeout(ms int) bool {
	max := ExecutionTimeoutHardMaxMS
	if c == ExecutionClassExtended {
		max = ExecutionExtendedTimeoutMaxMS
	}
	return c.Valid() && ms >= ExecutionTimeoutMinMS && ms <= max
}

func (l ExecutionPlanLimits) ForClass(c ExecutionClass) ExecutionPlanLimits {
	if !c.Valid() {
		return ExecutionPlanLimits{}
	}
	if c == ExecutionClassExtended {
		l.MaxTimeoutMS = l.ExtendedTimeoutMS
		l.DefaultTimeoutMS = l.ExtendedTimeoutMS
		l.MaxConcurrent = l.MaxExtendedConcurrent
		l.Allowed = l.Allowed && l.MaxTimeoutMS > 0 && l.MaxConcurrent > 0
	}
	return l
}
