from typing import Literal

SafeApplyProjectProjectStabilization = Literal["required"]

SAFE_APPLY_PROJECT_PROJECT_STABILIZATION_VALUES: set[SafeApplyProjectProjectStabilization] = {
    "required",
}


def check_safe_apply_project_project_stabilization(value: str) -> SafeApplyProjectProjectStabilization:
    if value in SAFE_APPLY_PROJECT_PROJECT_STABILIZATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_PROJECT_PROJECT_STABILIZATION_VALUES!r}")
