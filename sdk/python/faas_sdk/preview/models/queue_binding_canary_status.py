from typing import Literal

QueueBindingCanaryStatus = Literal["blocked", "cancelled", "dispatching", "expired", "failed", "published", "succeeded"]

QUEUE_BINDING_CANARY_STATUS_VALUES: set[QueueBindingCanaryStatus] = {
    "blocked",
    "cancelled",
    "dispatching",
    "expired",
    "failed",
    "published",
    "succeeded",
}


def check_queue_binding_canary_status(value: str) -> QueueBindingCanaryStatus:
    if value in QUEUE_BINDING_CANARY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_CANARY_STATUS_VALUES!r}")
