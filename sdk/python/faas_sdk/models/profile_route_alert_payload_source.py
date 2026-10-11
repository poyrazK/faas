from typing import Literal

ProfileRouteAlertPayloadSource = Literal["canary", "deployment", "periodic"]

PROFILE_ROUTE_ALERT_PAYLOAD_SOURCE_VALUES: set[ProfileRouteAlertPayloadSource] = {
    "canary",
    "deployment",
    "periodic",
}


def check_profile_route_alert_payload_source(value: str) -> ProfileRouteAlertPayloadSource:
    if value in PROFILE_ROUTE_ALERT_PAYLOAD_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_ROUTE_ALERT_PAYLOAD_SOURCE_VALUES!r}")
