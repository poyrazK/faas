from typing import Literal

ObjectWriteReceiptStatus = Literal["completed", "failed", "pending"]

OBJECT_WRITE_RECEIPT_STATUS_VALUES: set[ObjectWriteReceiptStatus] = {
    "completed",
    "failed",
    "pending",
}


def check_object_write_receipt_status(value: str) -> ObjectWriteReceiptStatus:
    if value in OBJECT_WRITE_RECEIPT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_WRITE_RECEIPT_STATUS_VALUES!r}")
