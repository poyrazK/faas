from typing import Literal

PublicStatusComponentStatus = Literal[
    "degraded", "maintenance", "major_outage", "operational", "partial_outage", "unknown"
]

PUBLIC_STATUS_COMPONENT_STATUS_VALUES: set[PublicStatusComponentStatus] = {
    "degraded",
    "maintenance",
    "major_outage",
    "operational",
    "partial_outage",
    "unknown",
}


def check_public_status_component_status(value: str) -> PublicStatusComponentStatus:
    if value in PUBLIC_STATUS_COMPONENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_COMPONENT_STATUS_VALUES!r}")
