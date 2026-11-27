from typing import Literal

DeleteExecutionEnvironmentResponse202Status = Literal["retiring"]

DELETE_EXECUTION_ENVIRONMENT_RESPONSE_202_STATUS_VALUES: set[DeleteExecutionEnvironmentResponse202Status] = {
    "retiring",
}


def check_delete_execution_environment_response_202_status(value: str) -> DeleteExecutionEnvironmentResponse202Status:
    if value in DELETE_EXECUTION_ENVIRONMENT_RESPONSE_202_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DELETE_EXECUTION_ENVIRONMENT_RESPONSE_202_STATUS_VALUES!r}"
    )
