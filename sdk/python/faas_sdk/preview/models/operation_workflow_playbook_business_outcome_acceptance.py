from typing import Literal

OperationWorkflowPlaybookBusinessOutcomeAcceptance = Literal[
    "acceptable_alternative", "expected", "pending", "unavailable", "unexpected"
]

OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_ACCEPTANCE_VALUES: set[
    OperationWorkflowPlaybookBusinessOutcomeAcceptance
] = {
    "acceptable_alternative",
    "expected",
    "pending",
    "unavailable",
    "unexpected",
}


def check_operation_workflow_playbook_business_outcome_acceptance(
    value: str,
) -> OperationWorkflowPlaybookBusinessOutcomeAcceptance:
    if value in OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_ACCEPTANCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_ACCEPTANCE_VALUES!r}"
    )
