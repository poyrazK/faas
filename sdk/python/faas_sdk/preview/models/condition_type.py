from typing import Literal

ConditionType = Literal["Degraded", "Progressing", "Ready", "RolledOut"]

CONDITION_TYPE_VALUES: set[ConditionType] = {
    "Degraded",
    "Progressing",
    "Ready",
    "RolledOut",
}


def check_condition_type(value: str) -> ConditionType:
    if value in CONDITION_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONDITION_TYPE_VALUES!r}")
