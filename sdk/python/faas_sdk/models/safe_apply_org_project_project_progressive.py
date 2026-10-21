from typing import Literal

SafeApplyOrgProjectProjectProgressive = Literal["required"]

SAFE_APPLY_ORG_PROJECT_PROJECT_PROGRESSIVE_VALUES: set[SafeApplyOrgProjectProjectProgressive] = {
    "required",
}


def check_safe_apply_org_project_project_progressive(value: str) -> SafeApplyOrgProjectProjectProgressive:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_PROGRESSIVE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_PROGRESSIVE_VALUES!r}"
    )
