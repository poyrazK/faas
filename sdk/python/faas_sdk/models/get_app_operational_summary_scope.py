from typing import Literal

GetAppOperationalSummaryScope = Literal["default"]

GET_APP_OPERATIONAL_SUMMARY_SCOPE_VALUES: set[GetAppOperationalSummaryScope] = {
    "default",
}


def check_get_app_operational_summary_scope(value: str) -> GetAppOperationalSummaryScope:
    if value in GET_APP_OPERATIONAL_SUMMARY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_OPERATIONAL_SUMMARY_SCOPE_VALUES!r}")
