from typing import Literal

ScanProjectProjectAddServices = Literal["required"]

SCAN_PROJECT_PROJECT_ADD_SERVICES_VALUES: set[ScanProjectProjectAddServices] = {
    "required",
}


def check_scan_project_project_add_services(value: str) -> ScanProjectProjectAddServices:
    if value in SCAN_PROJECT_PROJECT_ADD_SERVICES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_ADD_SERVICES_VALUES!r}")
