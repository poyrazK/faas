from typing import Literal

SafeApplyOrgProjectProjectStabilization = Literal["required"]

SAFE_APPLY_ORG_PROJECT_PROJECT_STABILIZATION_VALUES: set[SafeApplyOrgProjectProjectStabilization] = {
    "required",
}


def check_safe_apply_org_project_project_stabilization(value: str) -> SafeApplyOrgProjectProjectStabilization:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_STABILIZATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_STABILIZATION_VALUES!r}"
    )
