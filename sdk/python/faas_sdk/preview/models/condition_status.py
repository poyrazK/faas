from typing import Literal

ConditionStatus = Literal["False", "True", "Unknown"]

CONDITION_STATUS_VALUES: set[ConditionStatus] = {
    "False",
    "True",
    "Unknown",
}


def check_condition_status(value: str) -> ConditionStatus:
    if value in CONDITION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONDITION_STATUS_VALUES!r}")
