from typing import Literal

UpsertExclusiveOperationTriggerBindingSource = Literal["broker", "cron", "inbound_webhook", "job_schedule"]

UPSERT_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES: set[UpsertExclusiveOperationTriggerBindingSource] = {
    "broker",
    "cron",
    "inbound_webhook",
    "job_schedule",
}


def check_upsert_exclusive_operation_trigger_binding_source(value: str) -> UpsertExclusiveOperationTriggerBindingSource:
    if value in UPSERT_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPSERT_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES!r}"
    )
