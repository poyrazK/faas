from typing import Literal

PrewarmIntentResponseStatus = Literal["cancelled", "failed", "pending", "running", "succeeded"]

PREWARM_INTENT_RESPONSE_STATUS_VALUES: set[PrewarmIntentResponseStatus] = {
    "cancelled",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_prewarm_intent_response_status(value: str) -> PrewarmIntentResponseStatus:
    if value in PREWARM_INTENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREWARM_INTENT_RESPONSE_STATUS_VALUES!r}")
