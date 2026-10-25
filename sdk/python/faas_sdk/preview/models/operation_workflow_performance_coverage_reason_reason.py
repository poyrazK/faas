from typing import Literal

OperationWorkflowPerformanceCoverageReasonReason = Literal[
    "contract_changed",
    "duplicate_revision",
    "future_observation",
    "history_window_truncated",
    "missing_latest",
    "missing_start",
    "out_of_order_time",
    "revision_gap",
    "state_discontinuity",
    "verification_start_missing",
]

OPERATION_WORKFLOW_PERFORMANCE_COVERAGE_REASON_REASON_VALUES: set[OperationWorkflowPerformanceCoverageReasonReason] = {
    "contract_changed",
    "duplicate_revision",
    "future_observation",
    "history_window_truncated",
    "missing_latest",
    "missing_start",
    "out_of_order_time",
    "revision_gap",
    "state_discontinuity",
    "verification_start_missing",
}


def check_operation_workflow_performance_coverage_reason_reason(
    value: str,
) -> OperationWorkflowPerformanceCoverageReasonReason:
    if value in OPERATION_WORKFLOW_PERFORMANCE_COVERAGE_REASON_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_COVERAGE_REASON_REASON_VALUES!r}"
    )
