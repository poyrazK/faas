from typing import Literal

PublicStatusOverviewDataStatus = Literal["fresh", "stale", "unavailable"]

PUBLIC_STATUS_OVERVIEW_DATA_STATUS_VALUES: set[PublicStatusOverviewDataStatus] = {
    "fresh",
    "stale",
    "unavailable",
}


def check_public_status_overview_data_status(value: str) -> PublicStatusOverviewDataStatus:
    if value in PUBLIC_STATUS_OVERVIEW_DATA_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_OVERVIEW_DATA_STATUS_VALUES!r}")
