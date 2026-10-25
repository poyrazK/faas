from typing import Literal

TriggerSourceType3Type1 = Literal["delayed_task", "queue"]

TRIGGER_SOURCE_TYPE_3_TYPE_1_VALUES: set[TriggerSourceType3Type1] = {
    "delayed_task",
    "queue",
}


def check_trigger_source_type_3_type_1(value: str) -> TriggerSourceType3Type1:
    if value in TRIGGER_SOURCE_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_SOURCE_TYPE_3_TYPE_1_VALUES!r}")
