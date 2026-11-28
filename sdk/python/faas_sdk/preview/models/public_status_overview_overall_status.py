from typing import Literal

PublicStatusOverviewOverallStatus = Literal[
    "degraded", "maintenance", "major_outage", "operational", "partial_outage", "unknown"
]

PUBLIC_STATUS_OVERVIEW_OVERALL_STATUS_VALUES: set[PublicStatusOverviewOverallStatus] = {
    "degraded",
    "maintenance",
    "major_outage",
    "operational",
    "partial_outage",
    "unknown",
}


def check_public_status_overview_overall_status(value: str) -> PublicStatusOverviewOverallStatus:
    if value in PUBLIC_STATUS_OVERVIEW_OVERALL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_OVERVIEW_OVERALL_STATUS_VALUES!r}")
