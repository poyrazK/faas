from typing import Literal

OrgPlanResponsePlan = Literal["free", "hobby", "pro", "scale"]

ORG_PLAN_RESPONSE_PLAN_VALUES: set[OrgPlanResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_org_plan_response_plan(value: str) -> OrgPlanResponsePlan:
    if value in ORG_PLAN_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_PLAN_RESPONSE_PLAN_VALUES!r}")
