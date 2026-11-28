from typing import Literal

OrgResponsePlan = Literal["free", "hobby", "pro", "scale"]

ORG_RESPONSE_PLAN_VALUES: set[OrgResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_org_response_plan(value: str) -> OrgResponsePlan:
    if value in ORG_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_RESPONSE_PLAN_VALUES!r}")
