from typing import Literal

ResourceStatusKind = Literal["app", "cron", "database", "deployment", "domain", "job_run"]

RESOURCE_STATUS_KIND_VALUES: set[ResourceStatusKind] = {
    "app",
    "cron",
    "database",
    "deployment",
    "domain",
    "job_run",
}


def check_resource_status_kind(value: str) -> ResourceStatusKind:
    if value in RESOURCE_STATUS_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RESOURCE_STATUS_KIND_VALUES!r}")
