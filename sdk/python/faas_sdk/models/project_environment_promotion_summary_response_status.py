from typing import Literal

ProjectEnvironmentPromotionSummaryResponseStatus = Literal["failed", "running", "succeeded"]

PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentPromotionSummaryResponseStatus] = {
    "failed",
    "running",
    "succeeded",
}


def check_project_environment_promotion_summary_response_status(
    value: str,
) -> ProjectEnvironmentPromotionSummaryResponseStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_STATUS_VALUES!r}"
    )
