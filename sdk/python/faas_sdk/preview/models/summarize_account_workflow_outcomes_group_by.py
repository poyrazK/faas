from typing import Literal

SummarizeAccountWorkflowOutcomesGroupBy = Literal["customer", "outcome", "workflow"]

SUMMARIZE_ACCOUNT_WORKFLOW_OUTCOMES_GROUP_BY_VALUES: set[SummarizeAccountWorkflowOutcomesGroupBy] = {
    "customer",
    "outcome",
    "workflow",
}


def check_summarize_account_workflow_outcomes_group_by(value: str) -> SummarizeAccountWorkflowOutcomesGroupBy:
    if value in SUMMARIZE_ACCOUNT_WORKFLOW_OUTCOMES_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_ACCOUNT_WORKFLOW_OUTCOMES_GROUP_BY_VALUES!r}"
    )
