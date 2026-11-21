from typing import Literal

DeleteExecutionFileResponse202Status = Literal["cleanup_scheduled"]

DELETE_EXECUTION_FILE_RESPONSE_202_STATUS_VALUES: set[DeleteExecutionFileResponse202Status] = {
    "cleanup_scheduled",
}


def check_delete_execution_file_response_202_status(value: str) -> DeleteExecutionFileResponse202Status:
    if value in DELETE_EXECUTION_FILE_RESPONSE_202_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELETE_EXECUTION_FILE_RESPONSE_202_STATUS_VALUES!r}")
