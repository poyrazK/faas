from typing import Literal

ProjectPromotionControlStatus = Literal["aborted", "blocked", "pending", "restored"]

PROJECT_PROMOTION_CONTROL_STATUS_VALUES: set[ProjectPromotionControlStatus] = {
    "aborted",
    "blocked",
    "pending",
    "restored",
}


def check_project_promotion_control_status(value: str) -> ProjectPromotionControlStatus:
    if value in PROJECT_PROMOTION_CONTROL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_CONTROL_STATUS_VALUES!r}")
