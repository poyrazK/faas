from typing import Literal

DebugSuspectedDependencyReason = Literal["failures", "latency"]

DEBUG_SUSPECTED_DEPENDENCY_REASON_VALUES: set[DebugSuspectedDependencyReason] = {
    "failures",
    "latency",
}


def check_debug_suspected_dependency_reason(value: str) -> DebugSuspectedDependencyReason:
    if value in DEBUG_SUSPECTED_DEPENDENCY_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_SUSPECTED_DEPENDENCY_REASON_VALUES!r}")
