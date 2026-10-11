from typing import Literal

EventRecoveryPreviewCoverage = Literal["captured_application_recipients", "retained_application_executions"]

EVENT_RECOVERY_PREVIEW_COVERAGE_VALUES: set[EventRecoveryPreviewCoverage] = {
    "captured_application_recipients",
    "retained_application_executions",
}


def check_event_recovery_preview_coverage(value: str) -> EventRecoveryPreviewCoverage:
    if value in EVENT_RECOVERY_PREVIEW_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREVIEW_COVERAGE_VALUES!r}")
