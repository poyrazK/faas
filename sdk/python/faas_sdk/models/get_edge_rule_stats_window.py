from typing import Literal

GetEdgeRuleStatsWindow = Literal["1h", "24h", "7d"]

GET_EDGE_RULE_STATS_WINDOW_VALUES: set[GetEdgeRuleStatsWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_get_edge_rule_stats_window(value: str) -> GetEdgeRuleStatsWindow:
    if value in GET_EDGE_RULE_STATS_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EDGE_RULE_STATS_WINDOW_VALUES!r}")
