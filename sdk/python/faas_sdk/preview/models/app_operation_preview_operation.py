from typing import Literal

AppOperationPreviewOperation = Literal["deploy", "env_change", "restart", "scale"]

APP_OPERATION_PREVIEW_OPERATION_VALUES: set[AppOperationPreviewOperation] = {
    "deploy",
    "env_change",
    "restart",
    "scale",
}


def check_app_operation_preview_operation(value: str) -> AppOperationPreviewOperation:
    if value in APP_OPERATION_PREVIEW_OPERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATION_PREVIEW_OPERATION_VALUES!r}")
