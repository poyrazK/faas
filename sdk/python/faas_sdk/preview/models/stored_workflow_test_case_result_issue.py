from typing import Literal

StoredWorkflowTestCaseResultIssue = Literal[
    "assertion_failed", "definition_invalid", "invalid_expectations", "simulation_incomplete", "simulation_rejected"
]

STORED_WORKFLOW_TEST_CASE_RESULT_ISSUE_VALUES: set[StoredWorkflowTestCaseResultIssue] = {
    "assertion_failed",
    "definition_invalid",
    "invalid_expectations",
    "simulation_incomplete",
    "simulation_rejected",
}


def check_stored_workflow_test_case_result_issue(value: str) -> StoredWorkflowTestCaseResultIssue:
    if value in STORED_WORKFLOW_TEST_CASE_RESULT_ISSUE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_TEST_CASE_RESULT_ISSUE_VALUES!r}")
