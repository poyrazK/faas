from typing import Literal

FireCronResponseStatus = Literal["failed", "pending", "running", "succeeded"]

FIRE_CRON_RESPONSE_STATUS_VALUES: set[FireCronResponseStatus] = {
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_fire_cron_response_status(value: str) -> FireCronResponseStatus:
    if value in FIRE_CRON_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FIRE_CRON_RESPONSE_STATUS_VALUES!r}")
