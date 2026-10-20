from typing import Literal

AppOperationalSummaryVersion = Literal[1]

APP_OPERATIONAL_SUMMARY_VERSION_VALUES: set[AppOperationalSummaryVersion] = {
    1,
}


def check_app_operational_summary_version(value: int) -> AppOperationalSummaryVersion:
    if value in APP_OPERATIONAL_SUMMARY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATIONAL_SUMMARY_VERSION_VALUES!r}")
