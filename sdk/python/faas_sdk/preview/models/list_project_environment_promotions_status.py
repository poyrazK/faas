from typing import Literal

ListProjectEnvironmentPromotionsStatus = Literal["failed", "running", "succeeded"]

LIST_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES: set[ListProjectEnvironmentPromotionsStatus] = {
    "failed",
    "running",
    "succeeded",
}


def check_list_project_environment_promotions_status(value: str) -> ListProjectEnvironmentPromotionsStatus:
    if value in LIST_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES!r}"
    )
