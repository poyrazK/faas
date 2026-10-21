from typing import Literal

SafeApplyProjectProjectRemoveServices = Literal["required"]

SAFE_APPLY_PROJECT_PROJECT_REMOVE_SERVICES_VALUES: set[SafeApplyProjectProjectRemoveServices] = {
    "required",
}


def check_safe_apply_project_project_remove_services(value: str) -> SafeApplyProjectProjectRemoveServices:
    if value in SAFE_APPLY_PROJECT_PROJECT_REMOVE_SERVICES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_PROJECT_PROJECT_REMOVE_SERVICES_VALUES!r}"
    )
