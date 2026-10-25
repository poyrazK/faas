from typing import Literal

WorkflowRecoveryFailureCoverageCoverage = Literal["partial"]

WORKFLOW_RECOVERY_FAILURE_COVERAGE_COVERAGE_VALUES: set[WorkflowRecoveryFailureCoverageCoverage] = {
    "partial",
}


def check_workflow_recovery_failure_coverage_coverage(value: str) -> WorkflowRecoveryFailureCoverageCoverage:
    if value in WORKFLOW_RECOVERY_FAILURE_COVERAGE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_FAILURE_COVERAGE_COVERAGE_VALUES!r}"
    )
