from typing import Literal

PublicStatusUpdateImpact = Literal["degraded", "maintenance", "major_outage", "partial_outage"]

PUBLIC_STATUS_UPDATE_IMPACT_VALUES: set[PublicStatusUpdateImpact] = {
    "degraded",
    "maintenance",
    "major_outage",
    "partial_outage",
}


def check_public_status_update_impact(value: str) -> PublicStatusUpdateImpact:
    if value in PUBLIC_STATUS_UPDATE_IMPACT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_UPDATE_IMPACT_VALUES!r}")
