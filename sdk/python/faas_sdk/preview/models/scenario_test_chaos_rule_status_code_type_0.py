from typing import Literal

ScenarioTestChaosRuleStatusCodeType0 = Literal[429]

SCENARIO_TEST_CHAOS_RULE_STATUS_CODE_TYPE_0_VALUES: set[ScenarioTestChaosRuleStatusCodeType0] = {
    429,
}


def check_scenario_test_chaos_rule_status_code_type_0(value: int) -> ScenarioTestChaosRuleStatusCodeType0:
    if value in SCENARIO_TEST_CHAOS_RULE_STATUS_CODE_TYPE_0_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_TEST_CHAOS_RULE_STATUS_CODE_TYPE_0_VALUES!r}"
    )
