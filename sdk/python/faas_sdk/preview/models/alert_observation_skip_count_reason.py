from typing import Literal

AlertObservationSkipCountReason = Literal[
    "insufficient_history", "insufficient_traffic", "telemetry_missing_or_stale", "telemetry_unavailable"
]

ALERT_OBSERVATION_SKIP_COUNT_REASON_VALUES: set[AlertObservationSkipCountReason] = {
    "insufficient_history",
    "insufficient_traffic",
    "telemetry_missing_or_stale",
    "telemetry_unavailable",
}


def check_alert_observation_skip_count_reason(value: str) -> AlertObservationSkipCountReason:
    if value in ALERT_OBSERVATION_SKIP_COUNT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_OBSERVATION_SKIP_COUNT_REASON_VALUES!r}")
