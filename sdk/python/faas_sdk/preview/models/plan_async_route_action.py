from typing import Literal

PlanAsyncRouteAction = Literal["create", "remove", "skipped", "unchanged", "update"]

PLAN_ASYNC_ROUTE_ACTION_VALUES: set[PlanAsyncRouteAction] = {
    "create",
    "remove",
    "skipped",
    "unchanged",
    "update",
}


def check_plan_async_route_action(value: str) -> PlanAsyncRouteAction:
    if value in PLAN_ASYNC_ROUTE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_ASYNC_ROUTE_ACTION_VALUES!r}")
