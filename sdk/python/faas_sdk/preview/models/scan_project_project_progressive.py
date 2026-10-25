from typing import Literal

ScanProjectProjectProgressive = Literal["required"]

SCAN_PROJECT_PROJECT_PROGRESSIVE_VALUES: set[ScanProjectProjectProgressive] = {
    "required",
}


def check_scan_project_project_progressive(value: str) -> ScanProjectProjectProgressive:
    if value in SCAN_PROJECT_PROJECT_PROGRESSIVE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_PROGRESSIVE_VALUES!r}")
