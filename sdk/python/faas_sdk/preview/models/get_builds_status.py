from typing import Literal

GetBuildsStatus = Literal["failed", "queued", "running", "succeeded"]

GET_BUILDS_STATUS_VALUES: set[GetBuildsStatus] = {
    "failed",
    "queued",
    "running",
    "succeeded",
}


def check_get_builds_status(value: str) -> GetBuildsStatus:
    if value in GET_BUILDS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_BUILDS_STATUS_VALUES!r}")
