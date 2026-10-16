from typing import Literal

ScanProjectProjectReleaseMode = Literal[
    "atomic",
    "atomic-config-migrate-verify",
    "atomic-config-migrate-verify-workers",
    "atomic-config-verify",
    "atomic-config-verify-workers",
    "atomic-migrate-verify",
    "atomic-migrate-verify-workers",
    "atomic-verify",
    "atomic-verify-workers",
]

SCAN_PROJECT_PROJECT_RELEASE_MODE_VALUES: set[ScanProjectProjectReleaseMode] = {
    "atomic",
    "atomic-config-migrate-verify",
    "atomic-config-migrate-verify-workers",
    "atomic-config-verify",
    "atomic-config-verify-workers",
    "atomic-migrate-verify",
    "atomic-migrate-verify-workers",
    "atomic-verify",
    "atomic-verify-workers",
}


def check_scan_project_project_release_mode(value: str) -> ScanProjectProjectReleaseMode:
    if value in SCAN_PROJECT_PROJECT_RELEASE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_RELEASE_MODE_VALUES!r}")
