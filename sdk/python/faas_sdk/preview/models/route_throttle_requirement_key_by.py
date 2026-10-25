from typing import Literal

RouteThrottleRequirementKeyBy = Literal["api_key", "consumer_id", "country", "ip", "jwt_subject", "none"]

ROUTE_THROTTLE_REQUIREMENT_KEY_BY_VALUES: set[RouteThrottleRequirementKeyBy] = {
    "api_key",
    "consumer_id",
    "country",
    "ip",
    "jwt_subject",
    "none",
}


def check_route_throttle_requirement_key_by(value: str) -> RouteThrottleRequirementKeyBy:
    if value in ROUTE_THROTTLE_REQUIREMENT_KEY_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_THROTTLE_REQUIREMENT_KEY_BY_VALUES!r}")
