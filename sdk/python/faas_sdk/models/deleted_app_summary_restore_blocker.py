from typing import Literal

DeletedAppSummaryRestoreBlocker = Literal["", "grace_expired", "metadata_unavailable", "purge_started"]

DELETED_APP_SUMMARY_RESTORE_BLOCKER_VALUES: set[DeletedAppSummaryRestoreBlocker] = {
    "",
    "grace_expired",
    "metadata_unavailable",
    "purge_started",
}


def check_deleted_app_summary_restore_blocker(value: str) -> DeletedAppSummaryRestoreBlocker:
    if value in DELETED_APP_SUMMARY_RESTORE_BLOCKER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELETED_APP_SUMMARY_RESTORE_BLOCKER_VALUES!r}")
