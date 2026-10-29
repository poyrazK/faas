from typing import Literal

CronResponseKind = Literal["command", "http"]

CRON_RESPONSE_KIND_VALUES: set[CronResponseKind] = {
    "command",
    "http",
}


def check_cron_response_kind(value: str) -> CronResponseKind:
    if value in CRON_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CRON_RESPONSE_KIND_VALUES!r}")
