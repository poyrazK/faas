from typing import Literal

SafeApplyOrgProjectProjectWorkerHealth = Literal["required"]

SAFE_APPLY_ORG_PROJECT_PROJECT_WORKER_HEALTH_VALUES: set[SafeApplyOrgProjectProjectWorkerHealth] = {
    "required",
}


def check_safe_apply_org_project_project_worker_health(value: str) -> SafeApplyOrgProjectProjectWorkerHealth:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_WORKER_HEALTH_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_WORKER_HEALTH_VALUES!r}"
    )
