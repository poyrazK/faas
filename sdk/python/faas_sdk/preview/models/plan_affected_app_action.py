from typing import Literal

PlanAffectedAppAction = Literal["create", "noop", "remove", "update"]

PLAN_AFFECTED_APP_ACTION_VALUES: set[PlanAffectedAppAction] = {
    "create",
    "noop",
    "remove",
    "update",
}


def check_plan_affected_app_action(value: str) -> PlanAffectedAppAction:
    if value in PLAN_AFFECTED_APP_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_AFFECTED_APP_ACTION_VALUES!r}")
