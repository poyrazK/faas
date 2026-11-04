from typing import Literal

EventRecoveryJobCoverage = Literal[
    "captured_application_recipients", "retained_application_executions", "reviewed_workflow_runs"
]

EVENT_RECOVERY_JOB_COVERAGE_VALUES: set[EventRecoveryJobCoverage] = {
    "captured_application_recipients",
    "retained_application_executions",
    "reviewed_workflow_runs",
}


def check_event_recovery_job_coverage(value: str) -> EventRecoveryJobCoverage:
    if value in EVENT_RECOVERY_JOB_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_COVERAGE_VALUES!r}")
