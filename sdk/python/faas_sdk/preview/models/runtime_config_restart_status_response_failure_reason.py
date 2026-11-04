from typing import Literal

RuntimeConfigRestartStatusResponseFailureReason = Literal[
    "quiet_period_not_elapsed", "requests_active", "restart_attempt_failed", "telemetry_missing"
]

RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_FAILURE_REASON_VALUES: set[RuntimeConfigRestartStatusResponseFailureReason] = {
    "quiet_period_not_elapsed",
    "requests_active",
    "restart_attempt_failed",
    "telemetry_missing",
}


def check_runtime_config_restart_status_response_failure_reason(
    value: str,
) -> RuntimeConfigRestartStatusResponseFailureReason:
    if value in RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_FAILURE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_FAILURE_REASON_VALUES!r}"
    )
