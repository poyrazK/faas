from typing import Literal

EdgeRuleStatsResponseWindow = Literal["1h", "24h", "7d"]

EDGE_RULE_STATS_RESPONSE_WINDOW_VALUES: set[EdgeRuleStatsResponseWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_edge_rule_stats_response_window(value: str) -> EdgeRuleStatsResponseWindow:
    if value in EDGE_RULE_STATS_RESPONSE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_STATS_RESPONSE_WINDOW_VALUES!r}")
