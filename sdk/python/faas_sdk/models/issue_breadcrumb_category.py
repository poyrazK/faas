from typing import Literal

IssueBreadcrumbCategory = Literal["cache", "custom", "db", "http", "log", "workflow"]

ISSUE_BREADCRUMB_CATEGORY_VALUES: set[IssueBreadcrumbCategory] = {
    "cache",
    "custom",
    "db",
    "http",
    "log",
    "workflow",
}


def check_issue_breadcrumb_category(value: str) -> IssueBreadcrumbCategory:
    if value in ISSUE_BREADCRUMB_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_BREADCRUMB_CATEGORY_VALUES!r}")
