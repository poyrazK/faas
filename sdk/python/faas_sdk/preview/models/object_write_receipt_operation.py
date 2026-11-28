from typing import Literal

ObjectWriteReceiptOperation = Literal["copy", "put", "upload"]

OBJECT_WRITE_RECEIPT_OPERATION_VALUES: set[ObjectWriteReceiptOperation] = {
    "copy",
    "put",
    "upload",
}


def check_object_write_receipt_operation(value: str) -> ObjectWriteReceiptOperation:
    if value in OBJECT_WRITE_RECEIPT_OPERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_WRITE_RECEIPT_OPERATION_VALUES!r}")
