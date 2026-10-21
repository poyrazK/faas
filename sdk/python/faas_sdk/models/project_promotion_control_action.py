from typing import Literal

ProjectPromotionControlAction = Literal["abort", "recover"]

PROJECT_PROMOTION_CONTROL_ACTION_VALUES: set[ProjectPromotionControlAction] = {
    "abort",
    "recover",
}


def check_project_promotion_control_action(value: str) -> ProjectPromotionControlAction:
    if value in PROJECT_PROMOTION_CONTROL_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_CONTROL_ACTION_VALUES!r}")
