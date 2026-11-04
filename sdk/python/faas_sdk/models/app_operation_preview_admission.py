from typing import Literal

AppOperationPreviewAdmission = Literal["allowed", "blocked", "unknown"]

APP_OPERATION_PREVIEW_ADMISSION_VALUES: set[AppOperationPreviewAdmission] = {
    "allowed",
    "blocked",
    "unknown",
}


def check_app_operation_preview_admission(value: str) -> AppOperationPreviewAdmission:
    if value in APP_OPERATION_PREVIEW_ADMISSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATION_PREVIEW_ADMISSION_VALUES!r}")
