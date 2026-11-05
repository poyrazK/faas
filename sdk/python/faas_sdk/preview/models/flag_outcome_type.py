from typing import Literal

FlagOutcomeType = Literal["boolean", "variant"]

FLAG_OUTCOME_TYPE_VALUES: set[FlagOutcomeType] = {
    "boolean",
    "variant",
}


def check_flag_outcome_type(value: str) -> FlagOutcomeType:
    if value in FLAG_OUTCOME_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_OUTCOME_TYPE_VALUES!r}")
