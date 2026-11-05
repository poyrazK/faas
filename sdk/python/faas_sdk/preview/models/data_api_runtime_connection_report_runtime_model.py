from typing import Literal

DataAPIRuntimeConnectionReportRuntimeModel = Literal["postgrest-14.3-pools-v1"]

DATA_API_RUNTIME_CONNECTION_REPORT_RUNTIME_MODEL_VALUES: set[DataAPIRuntimeConnectionReportRuntimeModel] = {
    "postgrest-14.3-pools-v1",
}


def check_data_api_runtime_connection_report_runtime_model(value: str) -> DataAPIRuntimeConnectionReportRuntimeModel:
    if value in DATA_API_RUNTIME_CONNECTION_REPORT_RUNTIME_MODEL_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DATA_API_RUNTIME_CONNECTION_REPORT_RUNTIME_MODEL_VALUES!r}"
    )
