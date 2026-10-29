from typing import Literal

ProfileRouteAlertPayloadVersion = Literal[1]

PROFILE_ROUTE_ALERT_PAYLOAD_VERSION_VALUES: set[ProfileRouteAlertPayloadVersion] = {
    1,
}


def check_profile_route_alert_payload_version(value: int) -> ProfileRouteAlertPayloadVersion:
    if value in PROFILE_ROUTE_ALERT_PAYLOAD_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_ROUTE_ALERT_PAYLOAD_VERSION_VALUES!r}")
