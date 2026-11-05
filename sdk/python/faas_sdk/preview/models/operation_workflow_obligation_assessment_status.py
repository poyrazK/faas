from typing import Literal

OperationWorkflowObligationAssessmentStatus = Literal[
    "acceptance_required",
    "fulfillment_unassessed",
    "human_action_required",
    "review_actionable",
    "review_evidence_unavailable",
    "scheduled_check",
    "target_requirements_satisfied",
    "target_unavailable",
    "waiting_on_target_requirements",
]

OPERATION_WORKFLOW_OBLIGATION_ASSESSMENT_STATUS_VALUES: set[OperationWorkflowObligationAssessmentStatus] = {
    "acceptance_required",
    "fulfillment_unassessed",
    "human_action_required",
    "review_actionable",
    "review_evidence_unavailable",
    "scheduled_check",
    "target_requirements_satisfied",
    "target_unavailable",
    "waiting_on_target_requirements",
}


def check_operation_workflow_obligation_assessment_status(value: str) -> OperationWorkflowObligationAssessmentStatus:
    if value in OPERATION_WORKFLOW_OBLIGATION_ASSESSMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_ASSESSMENT_STATUS_VALUES!r}"
    )
