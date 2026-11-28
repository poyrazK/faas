from typing import Literal

FailureRulesVersion = Literal[1]

FAILURE_RULES_VERSION_VALUES: set[FailureRulesVersion] = {
    1,
}


def check_failure_rules_version(value: int) -> FailureRulesVersion:
    if value in FAILURE_RULES_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FAILURE_RULES_VERSION_VALUES!r}")
