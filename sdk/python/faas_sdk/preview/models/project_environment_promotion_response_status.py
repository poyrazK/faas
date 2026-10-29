from typing import Literal

ProjectEnvironmentPromotionResponseStatus = Literal["failed", "running", "succeeded"]

PROJECT_ENVIRONMENT_PROMOTION_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentPromotionResponseStatus] = {
    "failed",
    "running",
    "succeeded",
}


def check_project_environment_promotion_response_status(value: str) -> ProjectEnvironmentPromotionResponseStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_RESPONSE_STATUS_VALUES!r}"
    )
