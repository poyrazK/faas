from typing import Literal

FlagDecisionSource = Literal["configuration", "fallback", "inherited"]

FLAG_DECISION_SOURCE_VALUES: set[FlagDecisionSource] = {
    "configuration",
    "fallback",
    "inherited",
}


def check_flag_decision_source(value: str) -> FlagDecisionSource:
    if value in FLAG_DECISION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_DECISION_SOURCE_VALUES!r}")
