from typing import Literal

ListOrgProjectEnvironmentPromotionsStatus = Literal["failed", "running", "succeeded"]

LIST_ORG_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES: set[ListOrgProjectEnvironmentPromotionsStatus] = {
    "failed",
    "running",
    "succeeded",
}


def check_list_org_project_environment_promotions_status(value: str) -> ListOrgProjectEnvironmentPromotionsStatus:
    if value in LIST_ORG_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ORG_PROJECT_ENVIRONMENT_PROMOTIONS_STATUS_VALUES!r}"
    )
