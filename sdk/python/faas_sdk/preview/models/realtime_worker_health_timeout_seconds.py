from typing import Literal

RealtimeWorkerHealthTimeoutSeconds = Literal[60]

REALTIME_WORKER_HEALTH_TIMEOUT_SECONDS_VALUES: set[RealtimeWorkerHealthTimeoutSeconds] = {
    60,
}


def check_realtime_worker_health_timeout_seconds(value: int) -> RealtimeWorkerHealthTimeoutSeconds:
    if value in REALTIME_WORKER_HEALTH_TIMEOUT_SECONDS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_WORKER_HEALTH_TIMEOUT_SECONDS_VALUES!r}")
