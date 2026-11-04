from typing import Literal

TriggerRecordState = Literal["claimed", "dead_letter", "pending", "retry", "succeeded"]

TRIGGER_RECORD_STATE_VALUES: set[TriggerRecordState] = {
    "claimed",
    "dead_letter",
    "pending",
    "retry",
    "succeeded",
}


def check_trigger_record_state(value: str) -> TriggerRecordState:
    if value in TRIGGER_RECORD_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_RECORD_STATE_VALUES!r}")
