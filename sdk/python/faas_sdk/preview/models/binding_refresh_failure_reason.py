from typing import Literal

BindingRefreshFailureReason = Literal[
    "quiet_period_not_elapsed", "requests_active", "restart_attempt_failed", "telemetry_missing"
]

BINDING_REFRESH_FAILURE_REASON_VALUES: set[BindingRefreshFailureReason] = {
    "quiet_period_not_elapsed",
    "requests_active",
    "restart_attempt_failed",
    "telemetry_missing",
}


def check_binding_refresh_failure_reason(value: str) -> BindingRefreshFailureReason:
    if value in BINDING_REFRESH_FAILURE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_REFRESH_FAILURE_REASON_VALUES!r}")
