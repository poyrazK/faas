from typing import Literal

RoutePolicyApplyResponseGatewayState = Literal["active", "converging", "unknown", "unobserved"]

ROUTE_POLICY_APPLY_RESPONSE_GATEWAY_STATE_VALUES: set[RoutePolicyApplyResponseGatewayState] = {
    "active",
    "converging",
    "unknown",
    "unobserved",
}


def check_route_policy_apply_response_gateway_state(value: str) -> RoutePolicyApplyResponseGatewayState:
    if value in ROUTE_POLICY_APPLY_RESPONSE_GATEWAY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_POLICY_APPLY_RESPONSE_GATEWAY_STATE_VALUES!r}")
