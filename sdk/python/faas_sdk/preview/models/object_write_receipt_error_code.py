from typing import Literal

ObjectWriteReceiptErrorCode = Literal[
    "configuration",
    "dispatch_failed",
    "preparation_expired",
    "provider_write_rejected",
    "provider_write_uncertain",
    "usage_unavailable",
]

OBJECT_WRITE_RECEIPT_ERROR_CODE_VALUES: set[ObjectWriteReceiptErrorCode] = {
    "configuration",
    "dispatch_failed",
    "preparation_expired",
    "provider_write_rejected",
    "provider_write_uncertain",
    "usage_unavailable",
}


def check_object_write_receipt_error_code(value: str) -> ObjectWriteReceiptErrorCode:
    if value in OBJECT_WRITE_RECEIPT_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_WRITE_RECEIPT_ERROR_CODE_VALUES!r}")
