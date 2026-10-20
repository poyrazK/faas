from typing import Literal

DevSyncHistoryItemStatus = Literal["failed", "live"]

DEV_SYNC_HISTORY_ITEM_STATUS_VALUES: set[DevSyncHistoryItemStatus] = {
    "failed",
    "live",
}


def check_dev_sync_history_item_status(value: str) -> DevSyncHistoryItemStatus:
    if value in DEV_SYNC_HISTORY_ITEM_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_SYNC_HISTORY_ITEM_STATUS_VALUES!r}")
