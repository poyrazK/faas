from typing import Literal

OperationWorkflowDecisionOutcomeGroupKind = Literal["decision", "path"]

OPERATION_WORKFLOW_DECISION_OUTCOME_GROUP_KIND_VALUES: set[OperationWorkflowDecisionOutcomeGroupKind] = {
    "decision",
    "path",
}


def check_operation_workflow_decision_outcome_group_kind(value: str) -> OperationWorkflowDecisionOutcomeGroupKind:
    if value in OPERATION_WORKFLOW_DECISION_OUTCOME_GROUP_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DECISION_OUTCOME_GROUP_KIND_VALUES!r}"
    )
