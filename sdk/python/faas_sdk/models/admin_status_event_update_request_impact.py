from typing import Literal

AdminStatusEventUpdateRequestImpact = Literal["degraded", "maintenance", "major_outage", "partial_outage"]

ADMIN_STATUS_EVENT_UPDATE_REQUEST_IMPACT_VALUES: set[AdminStatusEventUpdateRequestImpact] = {
    "degraded",
    "maintenance",
    "major_outage",
    "partial_outage",
}


def check_admin_status_event_update_request_impact(value: str) -> AdminStatusEventUpdateRequestImpact:
    if value in ADMIN_STATUS_EVENT_UPDATE_REQUEST_IMPACT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_EVENT_UPDATE_REQUEST_IMPACT_VALUES!r}")
