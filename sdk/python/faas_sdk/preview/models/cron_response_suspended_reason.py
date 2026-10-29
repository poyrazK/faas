from typing import Literal

CronResponseSuspendedReason = Literal["no_live_deployment"]

CRON_RESPONSE_SUSPENDED_REASON_VALUES: set[CronResponseSuspendedReason] = {
    "no_live_deployment",
}


def check_cron_response_suspended_reason(value: str) -> CronResponseSuspendedReason:
    if value in CRON_RESPONSE_SUSPENDED_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CRON_RESPONSE_SUSPENDED_REASON_VALUES!r}")
