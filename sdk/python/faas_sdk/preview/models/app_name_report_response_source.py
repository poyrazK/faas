from typing import Literal

AppNameReportResponseSource = Literal["live", "preview"]

APP_NAME_REPORT_RESPONSE_SOURCE_VALUES: set[AppNameReportResponseSource] = {
    "live",
    "preview",
}


def check_app_name_report_response_source(value: str) -> AppNameReportResponseSource:
    if value in APP_NAME_REPORT_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_NAME_REPORT_RESPONSE_SOURCE_VALUES!r}")
