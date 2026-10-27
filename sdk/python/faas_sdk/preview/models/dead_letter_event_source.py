from typing import Literal

DeadLetterEventSource = Literal[
    "event_route", "invocation", "job_run", "trigger_record", "webhook_delivery", "workflow_run"
]

DEAD_LETTER_EVENT_SOURCE_VALUES: set[DeadLetterEventSource] = {
    "event_route",
    "invocation",
    "job_run",
    "trigger_record",
    "webhook_delivery",
    "workflow_run",
}


def check_dead_letter_event_source(value: str) -> DeadLetterEventSource:
    if value in DEAD_LETTER_EVENT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEAD_LETTER_EVENT_SOURCE_VALUES!r}")
