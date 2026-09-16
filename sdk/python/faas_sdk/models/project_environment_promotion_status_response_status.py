from typing import Literal

ProjectEnvironmentPromotionStatusResponseStatus = Literal["failed", "running", "succeeded"]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentPromotionStatusResponseStatus] = {
    "failed",
    "running",
    "succeeded",
}


def check_project_environment_promotion_status_response_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusResponseStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_STATUS_VALUES!r}"
    )
