from typing import Literal

UpdateAppRequestExecutionModeType2Type1 = Literal["job", "request", "service", "worker"]

UPDATE_APP_REQUEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestExecutionModeType2Type1] = {
    "job",
    "request",
    "service",
    "worker",
}


def check_update_app_request_execution_mode_type_2_type_1(value: str) -> UpdateAppRequestExecutionModeType2Type1:
    if value in UPDATE_APP_REQUEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES!r}"
    )
