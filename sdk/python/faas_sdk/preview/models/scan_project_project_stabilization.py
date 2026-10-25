from typing import Literal

ScanProjectProjectStabilization = Literal["required"]

SCAN_PROJECT_PROJECT_STABILIZATION_VALUES: set[ScanProjectProjectStabilization] = {
    "required",
}


def check_scan_project_project_stabilization(value: str) -> ScanProjectProjectStabilization:
    if value in SCAN_PROJECT_PROJECT_STABILIZATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_STABILIZATION_VALUES!r}")
