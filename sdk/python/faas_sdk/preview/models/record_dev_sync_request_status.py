from typing import Literal

RecordDevSyncRequestStatus = Literal["failed", "live"]

RECORD_DEV_SYNC_REQUEST_STATUS_VALUES: set[RecordDevSyncRequestStatus] = {
    "failed",
    "live",
}


def check_record_dev_sync_request_status(value: str) -> RecordDevSyncRequestStatus:
    if value in RECORD_DEV_SYNC_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RECORD_DEV_SYNC_REQUEST_STATUS_VALUES!r}")
