from typing import Literal

GetAppOperationPreviewOperation = Literal["deploy", "env_change", "restart", "scale"]

GET_APP_OPERATION_PREVIEW_OPERATION_VALUES: set[GetAppOperationPreviewOperation] = {
    "deploy",
    "env_change",
    "restart",
    "scale",
}


def check_get_app_operation_preview_operation(value: str) -> GetAppOperationPreviewOperation:
    if value in GET_APP_OPERATION_PREVIEW_OPERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_OPERATION_PREVIEW_OPERATION_VALUES!r}")
