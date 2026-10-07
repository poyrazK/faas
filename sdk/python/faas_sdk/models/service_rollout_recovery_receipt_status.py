from typing import Literal

ServiceRolloutRecoveryReceiptStatus = Literal["accepted"]

SERVICE_ROLLOUT_RECOVERY_RECEIPT_STATUS_VALUES: set[ServiceRolloutRecoveryReceiptStatus] = {
    "accepted",
}


def check_service_rollout_recovery_receipt_status(value: str) -> ServiceRolloutRecoveryReceiptStatus:
    if value in SERVICE_ROLLOUT_RECOVERY_RECEIPT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ROLLOUT_RECOVERY_RECEIPT_STATUS_VALUES!r}")
