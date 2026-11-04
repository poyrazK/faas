from typing import Literal

RouteRemovalPolicyMode = Literal["enforce", "report"]

ROUTE_REMOVAL_POLICY_MODE_VALUES: set[RouteRemovalPolicyMode] = {
    "enforce",
    "report",
}


def check_route_removal_policy_mode(value: str) -> RouteRemovalPolicyMode:
    if value in ROUTE_REMOVAL_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REMOVAL_POLICY_MODE_VALUES!r}")
