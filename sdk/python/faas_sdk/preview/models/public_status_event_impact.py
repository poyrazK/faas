from typing import Literal

PublicStatusEventImpact = Literal["degraded", "maintenance", "major_outage", "partial_outage"]

PUBLIC_STATUS_EVENT_IMPACT_VALUES: set[PublicStatusEventImpact] = {
    "degraded",
    "maintenance",
    "major_outage",
    "partial_outage",
}


def check_public_status_event_impact(value: str) -> PublicStatusEventImpact:
    if value in PUBLIC_STATUS_EVENT_IMPACT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_EVENT_IMPACT_VALUES!r}")
