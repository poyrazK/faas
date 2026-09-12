from typing import Literal

PublicStatusDailyStatus = Literal["degraded", "maintenance", "major_outage", "operational", "partial_outage", "unknown"]

PUBLIC_STATUS_DAILY_STATUS_VALUES: set[PublicStatusDailyStatus] = {
    "degraded",
    "maintenance",
    "major_outage",
    "operational",
    "partial_outage",
    "unknown",
}


def check_public_status_daily_status(value: str) -> PublicStatusDailyStatus:
    if value in PUBLIC_STATUS_DAILY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_DAILY_STATUS_VALUES!r}")
