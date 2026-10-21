from typing import Literal

ScanProjectProjectRemoveServices = Literal["required"]

SCAN_PROJECT_PROJECT_REMOVE_SERVICES_VALUES: set[ScanProjectProjectRemoveServices] = {
    "required",
}


def check_scan_project_project_remove_services(value: str) -> ScanProjectProjectRemoveServices:
    if value in SCAN_PROJECT_PROJECT_REMOVE_SERVICES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_REMOVE_SERVICES_VALUES!r}")
