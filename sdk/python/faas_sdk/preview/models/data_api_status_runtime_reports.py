from typing import Literal

DataAPIStatusRuntimeReports = Literal["available", "unavailable"]

DATA_API_STATUS_RUNTIME_REPORTS_VALUES: set[DataAPIStatusRuntimeReports] = {
    "available",
    "unavailable",
}


def check_data_api_status_runtime_reports(value: str) -> DataAPIStatusRuntimeReports:
    if value in DATA_API_STATUS_RUNTIME_REPORTS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_STATUS_RUNTIME_REPORTS_VALUES!r}")
