from typing import Literal

ScenarioTestChaosRuleKind = Literal[
    "http_status",
    "latency",
    "tcp_bandwidth",
    "tcp_connect_refused",
    "tcp_connect_timeout",
    "tcp_latency",
    "tcp_reset",
    "tcp_timeout",
]

SCENARIO_TEST_CHAOS_RULE_KIND_VALUES: set[ScenarioTestChaosRuleKind] = {
    "http_status",
    "latency",
    "tcp_bandwidth",
    "tcp_connect_refused",
    "tcp_connect_timeout",
    "tcp_latency",
    "tcp_reset",
    "tcp_timeout",
}


def check_scenario_test_chaos_rule_kind(value: str) -> ScenarioTestChaosRuleKind:
    if value in SCENARIO_TEST_CHAOS_RULE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_TEST_CHAOS_RULE_KIND_VALUES!r}")
