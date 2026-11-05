from typing import Literal

FlagRuleRolloutUnit = Literal["customer", "subject"]

FLAG_RULE_ROLLOUT_UNIT_VALUES: set[FlagRuleRolloutUnit] = {
    "customer",
    "subject",
}


def check_flag_rule_rollout_unit(value: str) -> FlagRuleRolloutUnit:
    if value in FLAG_RULE_ROLLOUT_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_RULE_ROLLOUT_UNIT_VALUES!r}")
