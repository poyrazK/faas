from typing import Literal

RoutePolicyPlanStatus = Literal["blocked", "no_changes", "partial", "ready"]

ROUTE_POLICY_PLAN_STATUS_VALUES: set[RoutePolicyPlanStatus] = {
    "blocked",
    "no_changes",
    "partial",
    "ready",
}


def check_route_policy_plan_status(value: str) -> RoutePolicyPlanStatus:
    if value in ROUTE_POLICY_PLAN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_POLICY_PLAN_STATUS_VALUES!r}")
