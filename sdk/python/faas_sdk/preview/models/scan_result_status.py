from typing import Literal

ScanResultStatus = Literal["complete", "failed", "pending", "skipped"]

SCAN_RESULT_STATUS_VALUES: set[ScanResultStatus] = {
    "complete",
    "failed",
    "pending",
    "skipped",
}


def check_scan_result_status(value: str) -> ScanResultStatus:
    if value in SCAN_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_RESULT_STATUS_VALUES!r}")
