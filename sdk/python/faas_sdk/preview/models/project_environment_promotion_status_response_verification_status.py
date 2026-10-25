from typing import Literal

ProjectEnvironmentPromotionStatusResponseVerificationStatus = Literal["failed", "pending", "verified", "verifying"]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_VERIFICATION_STATUS_VALUES: set[
    ProjectEnvironmentPromotionStatusResponseVerificationStatus
] = {
    "failed",
    "pending",
    "verified",
    "verifying",
}


def check_project_environment_promotion_status_response_verification_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusResponseVerificationStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_VERIFICATION_STATUS_VALUES!r}"
    )
