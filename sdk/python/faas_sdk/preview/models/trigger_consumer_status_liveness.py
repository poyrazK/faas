from typing import Literal

TriggerConsumerStatusLiveness = Literal["degraded", "healthy", "not_observed", "stale", "unknown"]

TRIGGER_CONSUMER_STATUS_LIVENESS_VALUES: set[TriggerConsumerStatusLiveness] = {
    "degraded",
    "healthy",
    "not_observed",
    "stale",
    "unknown",
}


def check_trigger_consumer_status_liveness(value: str) -> TriggerConsumerStatusLiveness:
    if value in TRIGGER_CONSUMER_STATUS_LIVENESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_CONSUMER_STATUS_LIVENESS_VALUES!r}")
