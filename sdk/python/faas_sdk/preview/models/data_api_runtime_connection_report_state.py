from typing import Literal

DataAPIRuntimeConnectionReportState = Literal["observed", "unknown"]

DATA_API_RUNTIME_CONNECTION_REPORT_STATE_VALUES: set[DataAPIRuntimeConnectionReportState] = {
    "observed",
    "unknown",
}


def check_data_api_runtime_connection_report_state(value: str) -> DataAPIRuntimeConnectionReportState:
    if value in DATA_API_RUNTIME_CONNECTION_REPORT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_RUNTIME_CONNECTION_REPORT_STATE_VALUES!r}")
