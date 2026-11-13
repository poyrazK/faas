from typing import Literal

StoredWorkflowCheckExclusionCode = Literal[
    "failure_route_missing",
    "guard_match_missing",
    "guard_skip_missing",
    "loop_empty_missing",
    "loop_multiple_items_missing",
    "retry_missing",
    "wait_success_missing",
    "wait_timeout_missing",
]

STORED_WORKFLOW_CHECK_EXCLUSION_CODE_VALUES: set[StoredWorkflowCheckExclusionCode] = {
    "failure_route_missing",
    "guard_match_missing",
    "guard_skip_missing",
    "loop_empty_missing",
    "loop_multiple_items_missing",
    "retry_missing",
    "wait_success_missing",
    "wait_timeout_missing",
}


def check_stored_workflow_check_exclusion_code(value: str) -> StoredWorkflowCheckExclusionCode:
    if value in STORED_WORKFLOW_CHECK_EXCLUSION_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_CHECK_EXCLUSION_CODE_VALUES!r}")
