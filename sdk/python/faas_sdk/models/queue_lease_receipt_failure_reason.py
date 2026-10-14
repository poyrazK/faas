from typing import Literal

QueueLeaseReceiptFailureReason = Literal["invalid_receipt", "lease_expired"]

QUEUE_LEASE_RECEIPT_FAILURE_REASON_VALUES: set[QueueLeaseReceiptFailureReason] = {
    "invalid_receipt",
    "lease_expired",
}


def check_queue_lease_receipt_failure_reason(value: str) -> QueueLeaseReceiptFailureReason:
    if value in QUEUE_LEASE_RECEIPT_FAILURE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_LEASE_RECEIPT_FAILURE_REASON_VALUES!r}")
