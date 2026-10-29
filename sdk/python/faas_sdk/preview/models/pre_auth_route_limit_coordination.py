from typing import Literal

PreAuthRouteLimitCoordination = Literal["central", "local"]

PRE_AUTH_ROUTE_LIMIT_COORDINATION_VALUES: set[PreAuthRouteLimitCoordination] = {
    "central",
    "local",
}


def check_pre_auth_route_limit_coordination(value: str) -> PreAuthRouteLimitCoordination:
    if value in PRE_AUTH_ROUTE_LIMIT_COORDINATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_ROUTE_LIMIT_COORDINATION_VALUES!r}")
