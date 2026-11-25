from typing import Literal

DataAPIInspectionResponseTaskStatus = Literal[
    "cancelled", "failed", "queued", "restoring", "running", "succeeded", "timed_out"
]

DATA_API_INSPECTION_RESPONSE_TASK_STATUS_VALUES: set[DataAPIInspectionResponseTaskStatus] = {
    "cancelled",
    "failed",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_data_api_inspection_response_task_status(value: str) -> DataAPIInspectionResponseTaskStatus:
    if value in DATA_API_INSPECTION_RESPONSE_TASK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_INSPECTION_RESPONSE_TASK_STATUS_VALUES!r}")
