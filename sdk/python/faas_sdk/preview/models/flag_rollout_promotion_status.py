from typing import Literal

FlagRolloutPromotionStatus = Literal["complete", "held", "promoted"]

FLAG_ROLLOUT_PROMOTION_STATUS_VALUES: set[FlagRolloutPromotionStatus] = {
    "complete",
    "held",
    "promoted",
}


def check_flag_rollout_promotion_status(value: str) -> FlagRolloutPromotionStatus:
    if value in FLAG_ROLLOUT_PROMOTION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_ROLLOUT_PROMOTION_STATUS_VALUES!r}")
