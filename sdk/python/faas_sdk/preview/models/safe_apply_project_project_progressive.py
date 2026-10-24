from typing import Literal

SafeApplyProjectProjectProgressive = Literal["required"]

SAFE_APPLY_PROJECT_PROJECT_PROGRESSIVE_VALUES: set[SafeApplyProjectProjectProgressive] = {
    "required",
}


def check_safe_apply_project_project_progressive(value: str) -> SafeApplyProjectProjectProgressive:
    if value in SAFE_APPLY_PROJECT_PROJECT_PROGRESSIVE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_PROJECT_PROJECT_PROGRESSIVE_VALUES!r}")
