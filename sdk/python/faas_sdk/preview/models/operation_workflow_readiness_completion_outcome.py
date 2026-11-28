from typing import Literal

OperationWorkflowReadinessCompletionOutcome = Literal["handed_off", "no_longer_applicable", "resolved"]

OPERATION_WORKFLOW_READINESS_COMPLETION_OUTCOME_VALUES: set[OperationWorkflowReadinessCompletionOutcome] = {
    "handed_off",
    "no_longer_applicable",
    "resolved",
}


def check_operation_workflow_readiness_completion_outcome(value: str) -> OperationWorkflowReadinessCompletionOutcome:
    if value in OPERATION_WORKFLOW_READINESS_COMPLETION_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COMPLETION_OUTCOME_VALUES!r}"
    )
