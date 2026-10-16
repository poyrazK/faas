from typing import Literal

RouteAdvicePolicyContextStatus = Literal["changed", "observed", "unavailable"]

ROUTE_ADVICE_POLICY_CONTEXT_STATUS_VALUES: set[RouteAdvicePolicyContextStatus] = {
    "changed",
    "observed",
    "unavailable",
}


def check_route_advice_policy_context_status(value: str) -> RouteAdvicePolicyContextStatus:
    if value in ROUTE_ADVICE_POLICY_CONTEXT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_POLICY_CONTEXT_STATUS_VALUES!r}")
