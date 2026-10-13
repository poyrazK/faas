from typing import Literal

OperationWorkflowGapQueueGuidanceStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

OPERATION_WORKFLOW_GAP_QUEUE_GUIDANCE_STATUS_VALUES: set[OperationWorkflowGapQueueGuidanceStatus] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_operation_workflow_gap_queue_guidance_status(value: str) -> OperationWorkflowGapQueueGuidanceStatus:
    if value in OPERATION_WORKFLOW_GAP_QUEUE_GUIDANCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_GAP_QUEUE_GUIDANCE_STATUS_VALUES!r}"
    )
