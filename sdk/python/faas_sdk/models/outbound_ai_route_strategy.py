from typing import Literal

OutboundAIRouteStrategy = Literal["cost", "latency", "ordered", "weighted"]

OUTBOUND_AI_ROUTE_STRATEGY_VALUES: set[OutboundAIRouteStrategy] = {
    "cost",
    "latency",
    "ordered",
    "weighted",
}


def check_outbound_ai_route_strategy(value: str) -> OutboundAIRouteStrategy:
    if value in OUTBOUND_AI_ROUTE_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_ROUTE_STRATEGY_VALUES!r}")
