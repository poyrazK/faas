from typing import Literal

CreateOutboundAIRouteRequestStrategy = Literal["cost", "latency", "ordered", "weighted"]

CREATE_OUTBOUND_AI_ROUTE_REQUEST_STRATEGY_VALUES: set[CreateOutboundAIRouteRequestStrategy] = {
    "cost",
    "latency",
    "ordered",
    "weighted",
}


def check_create_outbound_ai_route_request_strategy(value: str) -> CreateOutboundAIRouteRequestStrategy:
    if value in CREATE_OUTBOUND_AI_ROUTE_REQUEST_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_OUTBOUND_AI_ROUTE_REQUEST_STRATEGY_VALUES!r}")
