from typing import Literal

DeploymentPromotionHoldLastRecoveryOutcome = Literal["cancelled", "held", "inactive", "recovered", "retry"]

DEPLOYMENT_PROMOTION_HOLD_LAST_RECOVERY_OUTCOME_VALUES: set[DeploymentPromotionHoldLastRecoveryOutcome] = {
    "cancelled",
    "held",
    "inactive",
    "recovered",
    "retry",
}


def check_deployment_promotion_hold_last_recovery_outcome(value: str) -> DeploymentPromotionHoldLastRecoveryOutcome:
    if value in DEPLOYMENT_PROMOTION_HOLD_LAST_RECOVERY_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_PROMOTION_HOLD_LAST_RECOVERY_OUTCOME_VALUES!r}"
    )
