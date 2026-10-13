from typing import Literal

IssueBreadcrumbLevel = Literal["debug", "error", "info", "warning"]

ISSUE_BREADCRUMB_LEVEL_VALUES: set[IssueBreadcrumbLevel] = {
    "debug",
    "error",
    "info",
    "warning",
}


def check_issue_breadcrumb_level(value: str) -> IssueBreadcrumbLevel:
    if value in ISSUE_BREADCRUMB_LEVEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_BREADCRUMB_LEVEL_VALUES!r}")
