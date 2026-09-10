from typing import Literal

PublicStatusOverviewRegionScope = Literal["single-region"]

PUBLIC_STATUS_OVERVIEW_REGION_SCOPE_VALUES: set[PublicStatusOverviewRegionScope] = {
    "single-region",
}


def check_public_status_overview_region_scope(value: str) -> PublicStatusOverviewRegionScope:
    if value in PUBLIC_STATUS_OVERVIEW_REGION_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_OVERVIEW_REGION_SCOPE_VALUES!r}")
