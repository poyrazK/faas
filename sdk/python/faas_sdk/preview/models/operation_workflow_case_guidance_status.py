from typing import Literal

OperationWorkflowCaseGuidanceStatus = Literal[
    "available", "definition_unavailable", "evidence_unavailable", "not_declared"
]

OPERATION_WORKFLOW_CASE_GUIDANCE_STATUS_VALUES: set[OperationWorkflowCaseGuidanceStatus] = {
    "available",
    "definition_unavailable",
    "evidence_unavailable",
    "not_declared",
}


def check_operation_workflow_case_guidance_status(value: str) -> OperationWorkflowCaseGuidanceStatus:
    if value in OPERATION_WORKFLOW_CASE_GUIDANCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CASE_GUIDANCE_STATUS_VALUES!r}")
