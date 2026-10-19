from typing import Literal

DataAPIRuntimeConnectionReportVersion = Literal[1]

DATA_API_RUNTIME_CONNECTION_REPORT_VERSION_VALUES: set[DataAPIRuntimeConnectionReportVersion] = {
    1,
}


def check_data_api_runtime_connection_report_version(value: int) -> DataAPIRuntimeConnectionReportVersion:
    if value in DATA_API_RUNTIME_CONNECTION_REPORT_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DATA_API_RUNTIME_CONNECTION_REPORT_VERSION_VALUES!r}"
    )
