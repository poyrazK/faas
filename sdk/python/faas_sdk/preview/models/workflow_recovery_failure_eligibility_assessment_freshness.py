from typing import Literal

WorkflowRecoveryFailureEligibilityAssessmentFreshness = Literal["fresh", "stale"]

WORKFLOW_RECOVERY_FAILURE_ELIGIBILITY_ASSESSMENT_FRESHNESS_VALUES: set[
    WorkflowRecoveryFailureEligibilityAssessmentFreshness
] = {
    "fresh",
    "stale",
}


def check_workflow_recovery_failure_eligibility_assessment_freshness(
    value: str,
) -> WorkflowRecoveryFailureEligibilityAssessmentFreshness:
    if value in WORKFLOW_RECOVERY_FAILURE_ELIGIBILITY_ASSESSMENT_FRESHNESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_FAILURE_ELIGIBILITY_ASSESSMENT_FRESHNESS_VALUES!r}"
    )
