from typing import Literal

AutomationTestCaseResultIssue = Literal[
    "assertion_failed", "definition_invalid", "invalid_expectations", "simulation_incomplete", "simulation_rejected"
]

AUTOMATION_TEST_CASE_RESULT_ISSUE_VALUES: set[AutomationTestCaseResultIssue] = {
    "assertion_failed",
    "definition_invalid",
    "invalid_expectations",
    "simulation_incomplete",
    "simulation_rejected",
}


def check_automation_test_case_result_issue(value: str) -> AutomationTestCaseResultIssue:
    if value in AUTOMATION_TEST_CASE_RESULT_ISSUE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_TEST_CASE_RESULT_ISSUE_VALUES!r}")
