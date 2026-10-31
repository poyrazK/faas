from typing import Literal

SafeApplyProjectProjectAddServices = Literal["required"]

SAFE_APPLY_PROJECT_PROJECT_ADD_SERVICES_VALUES: set[SafeApplyProjectProjectAddServices] = {
    "required",
}


def check_safe_apply_project_project_add_services(value: str) -> SafeApplyProjectProjectAddServices:
    if value in SAFE_APPLY_PROJECT_PROJECT_ADD_SERVICES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_PROJECT_PROJECT_ADD_SERVICES_VALUES!r}")
