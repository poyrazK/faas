from typing import Literal

SafeApplyOrgProjectProjectRemoveServices = Literal["required"]

SAFE_APPLY_ORG_PROJECT_PROJECT_REMOVE_SERVICES_VALUES: set[SafeApplyOrgProjectProjectRemoveServices] = {
    "required",
}


def check_safe_apply_org_project_project_remove_services(value: str) -> SafeApplyOrgProjectProjectRemoveServices:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_REMOVE_SERVICES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_REMOVE_SERVICES_VALUES!r}"
    )
