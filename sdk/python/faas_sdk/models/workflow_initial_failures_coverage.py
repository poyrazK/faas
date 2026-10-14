from typing import Literal

WorkflowInitialFailuresCoverage = Literal["partial"]

WORKFLOW_INITIAL_FAILURES_COVERAGE_VALUES: set[WorkflowInitialFailuresCoverage] = {
    "partial",
}


def check_workflow_initial_failures_coverage(value: str) -> WorkflowInitialFailuresCoverage:
    if value in WORKFLOW_INITIAL_FAILURES_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_FAILURES_COVERAGE_VALUES!r}")
