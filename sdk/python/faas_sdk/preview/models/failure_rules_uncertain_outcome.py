from typing import Literal

FailureRulesUncertainOutcome = Literal["hold", "retry"]

FAILURE_RULES_UNCERTAIN_OUTCOME_VALUES: set[FailureRulesUncertainOutcome] = {
    "hold",
    "retry",
}


def check_failure_rules_uncertain_outcome(value: str) -> FailureRulesUncertainOutcome:
    if value in FAILURE_RULES_UNCERTAIN_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FAILURE_RULES_UNCERTAIN_OUTCOME_VALUES!r}")
