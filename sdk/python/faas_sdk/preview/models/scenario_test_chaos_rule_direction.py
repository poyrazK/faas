from typing import Literal

ScenarioTestChaosRuleDirection = Literal["both", "downstream", "upstream"]

SCENARIO_TEST_CHAOS_RULE_DIRECTION_VALUES: set[ScenarioTestChaosRuleDirection] = {
    "both",
    "downstream",
    "upstream",
}


def check_scenario_test_chaos_rule_direction(value: str) -> ScenarioTestChaosRuleDirection:
    if value in SCENARIO_TEST_CHAOS_RULE_DIRECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_TEST_CHAOS_RULE_DIRECTION_VALUES!r}")
