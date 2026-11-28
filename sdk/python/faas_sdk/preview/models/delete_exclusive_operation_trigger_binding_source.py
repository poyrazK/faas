from typing import Literal

DeleteExclusiveOperationTriggerBindingSource = Literal["broker", "cron", "inbound_webhook", "job_schedule"]

DELETE_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES: set[DeleteExclusiveOperationTriggerBindingSource] = {
    "broker",
    "cron",
    "inbound_webhook",
    "job_schedule",
}


def check_delete_exclusive_operation_trigger_binding_source(value: str) -> DeleteExclusiveOperationTriggerBindingSource:
    if value in DELETE_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DELETE_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES!r}"
    )
