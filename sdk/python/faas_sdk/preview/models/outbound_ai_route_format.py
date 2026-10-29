from typing import Literal

OutboundAIRouteFormat = Literal["native", "openai"]

OUTBOUND_AI_ROUTE_FORMAT_VALUES: set[OutboundAIRouteFormat] = {
    "native",
    "openai",
}


def check_outbound_ai_route_format(value: str) -> OutboundAIRouteFormat:
    if value in OUTBOUND_AI_ROUTE_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_ROUTE_FORMAT_VALUES!r}")
