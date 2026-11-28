from typing import Literal

RoutePolicyPlanVersion = Literal[2, 3]

ROUTE_POLICY_PLAN_VERSION_VALUES: set[RoutePolicyPlanVersion] = {
    2,
    3,
}


def check_route_policy_plan_version(value: int) -> RoutePolicyPlanVersion:
    if value in ROUTE_POLICY_PLAN_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_POLICY_PLAN_VERSION_VALUES!r}")
