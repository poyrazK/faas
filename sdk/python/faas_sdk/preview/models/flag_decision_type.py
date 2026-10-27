from typing import Literal

FlagDecisionType = Literal["boolean", "variant"]

FLAG_DECISION_TYPE_VALUES: set[FlagDecisionType] = {
    "boolean",
    "variant",
}


def check_flag_decision_type(value: str) -> FlagDecisionType:
    if value in FLAG_DECISION_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_DECISION_TYPE_VALUES!r}")
