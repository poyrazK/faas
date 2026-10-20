from typing import Literal

RoutePolicyPlanAuthority = Literal["server"]

ROUTE_POLICY_PLAN_AUTHORITY_VALUES: set[RoutePolicyPlanAuthority] = {
    "server",
}


def check_route_policy_plan_authority(value: str) -> RoutePolicyPlanAuthority:
    if value in ROUTE_POLICY_PLAN_AUTHORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_POLICY_PLAN_AUTHORITY_VALUES!r}")
