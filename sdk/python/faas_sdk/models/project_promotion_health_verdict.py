from typing import Literal

ProjectPromotionHealthVerdict = Literal["healthy", "regressed", "unknown"]

PROJECT_PROMOTION_HEALTH_VERDICT_VALUES: set[ProjectPromotionHealthVerdict] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_project_promotion_health_verdict(value: str) -> ProjectPromotionHealthVerdict:
    if value in PROJECT_PROMOTION_HEALTH_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_HEALTH_VERDICT_VALUES!r}")
