from typing import Literal

PrewarmIntentResponseTrigger = Literal["calendar", "cron", "pattern", "webhook"]

PREWARM_INTENT_RESPONSE_TRIGGER_VALUES: set[PrewarmIntentResponseTrigger] = {
    "calendar",
    "cron",
    "pattern",
    "webhook",
}


def check_prewarm_intent_response_trigger(value: str) -> PrewarmIntentResponseTrigger:
    if value in PREWARM_INTENT_RESPONSE_TRIGGER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREWARM_INTENT_RESPONSE_TRIGGER_VALUES!r}")
