from typing import Literal

ProjectEnvironmentPromotionStatusWorkloadResponseVerificationStatus = Literal["failed", "pending", "verified"]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_VERIFICATION_STATUS_VALUES: set[
    ProjectEnvironmentPromotionStatusWorkloadResponseVerificationStatus
] = {
    "failed",
    "pending",
    "verified",
}


def check_project_environment_promotion_status_workload_response_verification_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusWorkloadResponseVerificationStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_VERIFICATION_STATUS_VALUES!r}"
    )
