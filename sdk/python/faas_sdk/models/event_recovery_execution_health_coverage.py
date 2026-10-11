from typing import Literal

EventRecoveryExecutionHealthCoverage = Literal["bounded_retained_execution_jobs"]

EVENT_RECOVERY_EXECUTION_HEALTH_COVERAGE_VALUES: set[EventRecoveryExecutionHealthCoverage] = {
    "bounded_retained_execution_jobs",
}


def check_event_recovery_execution_health_coverage(value: str) -> EventRecoveryExecutionHealthCoverage:
    if value in EVENT_RECOVERY_EXECUTION_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_HEALTH_COVERAGE_VALUES!r}")
