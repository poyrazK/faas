from typing import Literal

WorkflowRecoveryInitialPolicyChangePreviewCoverage = Literal["partial"]

WORKFLOW_RECOVERY_INITIAL_POLICY_CHANGE_PREVIEW_COVERAGE_VALUES: set[
    WorkflowRecoveryInitialPolicyChangePreviewCoverage
] = {
    "partial",
}


def check_workflow_recovery_initial_policy_change_preview_coverage(
    value: str,
) -> WorkflowRecoveryInitialPolicyChangePreviewCoverage:
    if value in WORKFLOW_RECOVERY_INITIAL_POLICY_CHANGE_PREVIEW_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_INITIAL_POLICY_CHANGE_PREVIEW_COVERAGE_VALUES!r}"
    )
