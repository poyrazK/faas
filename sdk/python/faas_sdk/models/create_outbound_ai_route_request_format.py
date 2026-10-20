from typing import Literal

CreateOutboundAIRouteRequestFormat = Literal["native", "openai"]

CREATE_OUTBOUND_AI_ROUTE_REQUEST_FORMAT_VALUES: set[CreateOutboundAIRouteRequestFormat] = {
    "native",
    "openai",
}


def check_create_outbound_ai_route_request_format(value: str) -> CreateOutboundAIRouteRequestFormat:
    if value in CREATE_OUTBOUND_AI_ROUTE_REQUEST_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_OUTBOUND_AI_ROUTE_REQUEST_FORMAT_VALUES!r}")
