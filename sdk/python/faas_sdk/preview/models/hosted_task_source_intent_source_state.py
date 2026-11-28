from typing import Literal

HostedTaskSourceIntentSourceState = Literal["expired", "retained"]

HOSTED_TASK_SOURCE_INTENT_SOURCE_STATE_VALUES: set[HostedTaskSourceIntentSourceState] = {
    "expired",
    "retained",
}


def check_hosted_task_source_intent_source_state(value: str) -> HostedTaskSourceIntentSourceState:
    if value in HOSTED_TASK_SOURCE_INTENT_SOURCE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_INTENT_SOURCE_STATE_VALUES!r}")
