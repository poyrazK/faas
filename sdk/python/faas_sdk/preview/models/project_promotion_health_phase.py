from typing import Literal

ProjectPromotionHealthPhase = Literal[
    "aborted",
    "conflicted",
    "cutover",
    "failed",
    "finalizing",
    "migrating",
    "preflight",
    "queued",
    "restoring",
    "rollback_failed",
    "rolling_back",
    "succeeded",
    "verifying",
]

PROJECT_PROMOTION_HEALTH_PHASE_VALUES: set[ProjectPromotionHealthPhase] = {
    "aborted",
    "conflicted",
    "cutover",
    "failed",
    "finalizing",
    "migrating",
    "preflight",
    "queued",
    "restoring",
    "rollback_failed",
    "rolling_back",
    "succeeded",
    "verifying",
}


def check_project_promotion_health_phase(value: str) -> ProjectPromotionHealthPhase:
    if value in PROJECT_PROMOTION_HEALTH_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_HEALTH_PHASE_VALUES!r}")
