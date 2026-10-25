from typing import Literal

PatchOrgRequestPlan = Literal["free", "hobby", "pro", "scale"]

PATCH_ORG_REQUEST_PLAN_VALUES: set[PatchOrgRequestPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_patch_org_request_plan(value: str) -> PatchOrgRequestPlan:
    if value in PATCH_ORG_REQUEST_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PATCH_ORG_REQUEST_PLAN_VALUES!r}")
