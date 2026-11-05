from typing import Literal

FireCronRequestResponseStatus = Literal["cancelled", "failed", "pending", "running", "succeeded"]

FIRE_CRON_REQUEST_RESPONSE_STATUS_VALUES: set[FireCronRequestResponseStatus] = {
    "cancelled",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_fire_cron_request_response_status(value: str) -> FireCronRequestResponseStatus:
    if value in FIRE_CRON_REQUEST_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FIRE_CRON_REQUEST_RESPONSE_STATUS_VALUES!r}")
