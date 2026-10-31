from typing import Literal

ProjectEnvironmentPromotionSummaryResponseVerificationStatus = Literal["failed", "pending", "verified", "verifying"]

PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_VERIFICATION_STATUS_VALUES: set[
    ProjectEnvironmentPromotionSummaryResponseVerificationStatus
] = {
    "failed",
    "pending",
    "verified",
    "verifying",
}


def check_project_environment_promotion_summary_response_verification_status(
    value: str,
) -> ProjectEnvironmentPromotionSummaryResponseVerificationStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_VERIFICATION_STATUS_VALUES!r}"
    )
