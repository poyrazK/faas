from typing import Literal

ListObjectWriteReceiptsStatus = Literal["all", "completed", "failed", "pending"]

LIST_OBJECT_WRITE_RECEIPTS_STATUS_VALUES: set[ListObjectWriteReceiptsStatus] = {
    "all",
    "completed",
    "failed",
    "pending",
}


def check_list_object_write_receipts_status(value: str) -> ListObjectWriteReceiptsStatus:
    if value in LIST_OBJECT_WRITE_RECEIPTS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_OBJECT_WRITE_RECEIPTS_STATUS_VALUES!r}")
