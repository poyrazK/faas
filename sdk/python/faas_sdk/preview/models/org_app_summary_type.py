from typing import Literal

OrgAppSummaryType = Literal["app", "function"]

ORG_APP_SUMMARY_TYPE_VALUES: set[OrgAppSummaryType] = {
    "app",
    "function",
}


def check_org_app_summary_type(value: str) -> OrgAppSummaryType:
    if value in ORG_APP_SUMMARY_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_APP_SUMMARY_TYPE_VALUES!r}")
