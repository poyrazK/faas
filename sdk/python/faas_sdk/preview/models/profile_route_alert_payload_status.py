from typing import Literal

ProfileRouteAlertPayloadStatus = Literal["recovered", "regressed"]

PROFILE_ROUTE_ALERT_PAYLOAD_STATUS_VALUES: set[ProfileRouteAlertPayloadStatus] = {
    "recovered",
    "regressed",
}


def check_profile_route_alert_payload_status(value: str) -> ProfileRouteAlertPayloadStatus:
    if value in PROFILE_ROUTE_ALERT_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_ROUTE_ALERT_PAYLOAD_STATUS_VALUES!r}")
