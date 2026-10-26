from typing import Literal

FailureRulesUnmatchedFailure = Literal["fail_partition", "retry"]

FAILURE_RULES_UNMATCHED_FAILURE_VALUES: set[FailureRulesUnmatchedFailure] = {
    "fail_partition",
    "retry",
}


def check_failure_rules_unmatched_failure(value: str) -> FailureRulesUnmatchedFailure:
    if value in FAILURE_RULES_UNMATCHED_FAILURE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FAILURE_RULES_UNMATCHED_FAILURE_VALUES!r}")
