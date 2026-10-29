from typing import Literal

OperationWorkflowReadinessCompletionRequestOutcome = Literal["handed_off", "no_longer_applicable", "resolved"]

OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_OUTCOME_VALUES: set[
    OperationWorkflowReadinessCompletionRequestOutcome
] = {
    "handed_off",
    "no_longer_applicable",
    "resolved",
}


def check_operation_workflow_readiness_completion_request_outcome(
    value: str,
) -> OperationWorkflowReadinessCompletionRequestOutcome:
    if value in OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_OUTCOME_VALUES!r}"
    )
