from typing import Literal

FlagConditionOp = Literal["ends_with", "in", "not_in"]

FLAG_CONDITION_OP_VALUES: set[FlagConditionOp] = {
    "ends_with",
    "in",
    "not_in",
}


def check_flag_condition_op(value: str) -> FlagConditionOp:
    if value in FLAG_CONDITION_OP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_CONDITION_OP_VALUES!r}")
