from typing import Literal

PutOutboundAIRouteTargetsRequestStrategy = Literal["cost", "latency", "ordered", "weighted"]

PUT_OUTBOUND_AI_ROUTE_TARGETS_REQUEST_STRATEGY_VALUES: set[PutOutboundAIRouteTargetsRequestStrategy] = {
    "cost",
    "latency",
    "ordered",
    "weighted",
}


def check_put_outbound_ai_route_targets_request_strategy(value: str) -> PutOutboundAIRouteTargetsRequestStrategy:
    if value in PUT_OUTBOUND_AI_ROUTE_TARGETS_REQUEST_STRATEGY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_OUTBOUND_AI_ROUTE_TARGETS_REQUEST_STRATEGY_VALUES!r}"
    )
