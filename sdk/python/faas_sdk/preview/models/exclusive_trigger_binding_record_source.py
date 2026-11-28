from typing import Literal

ExclusiveTriggerBindingRecordSource = Literal["broker", "cron", "inbound_webhook", "job_schedule"]

EXCLUSIVE_TRIGGER_BINDING_RECORD_SOURCE_VALUES: set[ExclusiveTriggerBindingRecordSource] = {
    "broker",
    "cron",
    "inbound_webhook",
    "job_schedule",
}


def check_exclusive_trigger_binding_record_source(value: str) -> ExclusiveTriggerBindingRecordSource:
    if value in EXCLUSIVE_TRIGGER_BINDING_RECORD_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXCLUSIVE_TRIGGER_BINDING_RECORD_SOURCE_VALUES!r}")
