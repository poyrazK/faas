from typing import Literal

RouteThrottleRequirementMissingKeyPolicy = Literal["reject", "shared"]

ROUTE_THROTTLE_REQUIREMENT_MISSING_KEY_POLICY_VALUES: set[RouteThrottleRequirementMissingKeyPolicy] = {
    "reject",
    "shared",
}


def check_route_throttle_requirement_missing_key_policy(value: str) -> RouteThrottleRequirementMissingKeyPolicy:
    if value in ROUTE_THROTTLE_REQUIREMENT_MISSING_KEY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_THROTTLE_REQUIREMENT_MISSING_KEY_POLICY_VALUES!r}"
    )
