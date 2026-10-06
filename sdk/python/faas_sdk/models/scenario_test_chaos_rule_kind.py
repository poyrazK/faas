from typing import Literal

ScenarioTestChaosRuleKind = Literal["http_status", "latency"]

SCENARIO_TEST_CHAOS_RULE_KIND_VALUES: set[ScenarioTestChaosRuleKind] = {
    "http_status",
    "latency",
}


def check_scenario_test_chaos_rule_kind(value: str) -> ScenarioTestChaosRuleKind:
    if value in SCENARIO_TEST_CHAOS_RULE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_TEST_CHAOS_RULE_KIND_VALUES!r}")
