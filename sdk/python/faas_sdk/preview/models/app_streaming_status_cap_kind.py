from typing import Literal

AppStreamingStatusCapKind = Literal["endpoint-rule", "none", "plan"]

APP_STREAMING_STATUS_CAP_KIND_VALUES: set[AppStreamingStatusCapKind] = {
    "endpoint-rule",
    "none",
    "plan",
}


def check_app_streaming_status_cap_kind(value: str) -> AppStreamingStatusCapKind:
    if value in APP_STREAMING_STATUS_CAP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_STREAMING_STATUS_CAP_KIND_VALUES!r}")
