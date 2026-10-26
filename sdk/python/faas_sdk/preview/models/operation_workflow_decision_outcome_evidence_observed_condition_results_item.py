from typing import Literal

OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem = Literal["fail", "pass", "unknown"]

OPERATION_WORKFLOW_DECISION_OUTCOME_EVIDENCE_OBSERVED_CONDITION_RESULTS_ITEM_VALUES: set[
    OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem
] = {
    "fail",
    "pass",
    "unknown",
}


def check_operation_workflow_decision_outcome_evidence_observed_condition_results_item(
    value: str,
) -> OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem:
    if value in OPERATION_WORKFLOW_DECISION_OUTCOME_EVIDENCE_OBSERVED_CONDITION_RESULTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DECISION_OUTCOME_EVIDENCE_OBSERVED_CONDITION_RESULTS_ITEM_VALUES!r}"
    )
