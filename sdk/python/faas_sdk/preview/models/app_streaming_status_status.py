from typing import Literal

AppStreamingStatusStatus = Literal[
    "accept-json-downgrade", "flag-disabled", "operator-disabled", "plan-disallows", "streaming", "upgrade-bypass"
]

APP_STREAMING_STATUS_STATUS_VALUES: set[AppStreamingStatusStatus] = {
    "accept-json-downgrade",
    "flag-disabled",
    "operator-disabled",
    "plan-disallows",
    "streaming",
    "upgrade-bypass",
}


def check_app_streaming_status_status(value: str) -> AppStreamingStatusStatus:
    if value in APP_STREAMING_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_STREAMING_STATUS_STATUS_VALUES!r}")
