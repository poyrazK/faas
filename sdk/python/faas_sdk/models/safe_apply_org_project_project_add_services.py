from typing import Literal

SafeApplyOrgProjectProjectAddServices = Literal["required"]

SAFE_APPLY_ORG_PROJECT_PROJECT_ADD_SERVICES_VALUES: set[SafeApplyOrgProjectProjectAddServices] = {
    "required",
}


def check_safe_apply_org_project_project_add_services(value: str) -> SafeApplyOrgProjectProjectAddServices:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_ADD_SERVICES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_ADD_SERVICES_VALUES!r}"
    )
