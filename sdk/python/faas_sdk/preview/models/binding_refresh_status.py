from typing import Literal

BindingRefreshStatus = Literal["completed", "failed", "not_queued", "queued", "retrying", "running", "unknown"]

BINDING_REFRESH_STATUS_VALUES: set[BindingRefreshStatus] = {
    "completed",
    "failed",
    "not_queued",
    "queued",
    "retrying",
    "running",
    "unknown",
}


def check_binding_refresh_status(value: str) -> BindingRefreshStatus:
    if value in BINDING_REFRESH_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_REFRESH_STATUS_VALUES!r}")
