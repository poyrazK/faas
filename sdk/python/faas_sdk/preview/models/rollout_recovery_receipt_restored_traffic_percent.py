from typing import Literal

RolloutRecoveryReceiptRestoredTrafficPercent = Literal[100]

ROLLOUT_RECOVERY_RECEIPT_RESTORED_TRAFFIC_PERCENT_VALUES: set[RolloutRecoveryReceiptRestoredTrafficPercent] = {
    100,
}


def check_rollout_recovery_receipt_restored_traffic_percent(value: int) -> RolloutRecoveryReceiptRestoredTrafficPercent:
    if value in ROLLOUT_RECOVERY_RECEIPT_RESTORED_TRAFFIC_PERCENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROLLOUT_RECOVERY_RECEIPT_RESTORED_TRAFFIC_PERCENT_VALUES!r}"
    )
