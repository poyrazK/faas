from typing import Literal

SafeApplyProjectProjectWorkerHealth = Literal["required"]

SAFE_APPLY_PROJECT_PROJECT_WORKER_HEALTH_VALUES: set[SafeApplyProjectProjectWorkerHealth] = {
    "required",
}


def check_safe_apply_project_project_worker_health(value: str) -> SafeApplyProjectProjectWorkerHealth:
    if value in SAFE_APPLY_PROJECT_PROJECT_WORKER_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_PROJECT_PROJECT_WORKER_HEALTH_VALUES!r}")
