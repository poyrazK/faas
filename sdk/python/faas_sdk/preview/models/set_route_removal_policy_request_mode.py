from typing import Literal

SetRouteRemovalPolicyRequestMode = Literal["enforce", "report"]

SET_ROUTE_REMOVAL_POLICY_REQUEST_MODE_VALUES: set[SetRouteRemovalPolicyRequestMode] = {
    "enforce",
    "report",
}


def check_set_route_removal_policy_request_mode(value: str) -> SetRouteRemovalPolicyRequestMode:
    if value in SET_ROUTE_REMOVAL_POLICY_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_ROUTE_REMOVAL_POLICY_REQUEST_MODE_VALUES!r}")
