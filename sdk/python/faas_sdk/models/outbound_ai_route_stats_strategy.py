from typing import Literal

OutboundAIRouteStatsStrategy = Literal["cost", "latency", "ordered", "weighted"]

OUTBOUND_AI_ROUTE_STATS_STRATEGY_VALUES: set[OutboundAIRouteStatsStrategy] = {
    "cost",
    "latency",
    "ordered",
    "weighted",
}


def check_outbound_ai_route_stats_strategy(value: str) -> OutboundAIRouteStatsStrategy:
    if value in OUTBOUND_AI_ROUTE_STATS_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_ROUTE_STATS_STRATEGY_VALUES!r}")
