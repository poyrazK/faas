from typing import Literal

OrganizationSupportListStatus = Literal["open", "pending", "resolved"]

ORGANIZATION_SUPPORT_LIST_STATUS_VALUES: set[OrganizationSupportListStatus] = {
    "open",
    "pending",
    "resolved",
}


def check_organization_support_list_status(value: str) -> OrganizationSupportListStatus:
    if value in ORGANIZATION_SUPPORT_LIST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORGANIZATION_SUPPORT_LIST_STATUS_VALUES!r}")
