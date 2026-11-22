from typing import Literal

StoredWorkflowLiveSuiteCaseComparisonChange = Literal[
    "added", "incompatible", "recovery", "regression", "removed", "unchanged", "unknown"
]

STORED_WORKFLOW_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES: set[StoredWorkflowLiveSuiteCaseComparisonChange] = {
    "added",
    "incompatible",
    "recovery",
    "regression",
    "removed",
    "unchanged",
    "unknown",
}


def check_stored_workflow_live_suite_case_comparison_change(value: str) -> StoredWorkflowLiveSuiteCaseComparisonChange:
    if value in STORED_WORKFLOW_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES!r}"
    )
