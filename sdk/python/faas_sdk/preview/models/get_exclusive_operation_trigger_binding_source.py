from typing import Literal

GetExclusiveOperationTriggerBindingSource = Literal["broker", "cron", "inbound_webhook", "job_schedule"]

GET_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES: set[GetExclusiveOperationTriggerBindingSource] = {
    "broker",
    "cron",
    "inbound_webhook",
    "job_schedule",
}


def check_get_exclusive_operation_trigger_binding_source(value: str) -> GetExclusiveOperationTriggerBindingSource:
    if value in GET_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_EXCLUSIVE_OPERATION_TRIGGER_BINDING_SOURCE_VALUES!r}"
    )
