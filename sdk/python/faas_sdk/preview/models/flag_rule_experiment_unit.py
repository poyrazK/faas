from typing import Literal

FlagRuleExperimentUnit = Literal["customer", "subject"]

FLAG_RULE_EXPERIMENT_UNIT_VALUES: set[FlagRuleExperimentUnit] = {
    "customer",
    "subject",
}


def check_flag_rule_experiment_unit(value: str) -> FlagRuleExperimentUnit:
    if value in FLAG_RULE_EXPERIMENT_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_RULE_EXPERIMENT_UNIT_VALUES!r}")
