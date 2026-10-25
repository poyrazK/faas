from typing import Literal

OutboundAIRouteCanaryState = Literal["active", "rolled_back"]

OUTBOUND_AI_ROUTE_CANARY_STATE_VALUES: set[OutboundAIRouteCanaryState] = {
    "active",
    "rolled_back",
}


def check_outbound_ai_route_canary_state(value: str) -> OutboundAIRouteCanaryState:
    if value in OUTBOUND_AI_ROUTE_CANARY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_ROUTE_CANARY_STATE_VALUES!r}")
