from typing import Literal

ScenarioTestChaosRuleKind = Literal[
    "dns_latency",
    "dns_nxdomain",
    "dns_servfail",
    "dns_timeout",
    "http_malformed_json",
    "http_response_lost",
    "http_status",
    "http_truncated_body",
    "latency",
    "tcp_bandwidth",
    "tcp_connect_refused",
    "tcp_connect_timeout",
    "tcp_latency",
    "tcp_reset",
    "tcp_timeout",
]

SCENARIO_TEST_CHAOS_RULE_KIND_VALUES: set[ScenarioTestChaosRuleKind] = {
    "dns_latency",
    "dns_nxdomain",
    "dns_servfail",
    "dns_timeout",
    "http_malformed_json",
    "http_response_lost",
    "http_status",
    "http_truncated_body",
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
