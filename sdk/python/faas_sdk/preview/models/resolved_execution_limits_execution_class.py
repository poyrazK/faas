from typing import Literal

ResolvedExecutionLimitsExecutionClass = Literal["extended", "standard"]

RESOLVED_EXECUTION_LIMITS_EXECUTION_CLASS_VALUES: set[ResolvedExecutionLimitsExecutionClass] = {
    "extended",
    "standard",
}


def check_resolved_execution_limits_execution_class(value: str) -> ResolvedExecutionLimitsExecutionClass:
    if value in RESOLVED_EXECUTION_LIMITS_EXECUTION_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RESOLVED_EXECUTION_LIMITS_EXECUTION_CLASS_VALUES!r}")
