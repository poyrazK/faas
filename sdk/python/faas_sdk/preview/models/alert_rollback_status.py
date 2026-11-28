from typing import Literal

AlertRollbackStatus = Literal["blocked", "complete", "failed", "pending"]

ALERT_ROLLBACK_STATUS_VALUES: set[AlertRollbackStatus] = {
    "blocked",
    "complete",
    "failed",
    "pending",
}


def check_alert_rollback_status(value: str) -> AlertRollbackStatus:
    if value in ALERT_ROLLBACK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_ROLLBACK_STATUS_VALUES!r}")
