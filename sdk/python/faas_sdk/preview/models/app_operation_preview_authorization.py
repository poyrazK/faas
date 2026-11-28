from typing import Literal

AppOperationPreviewAuthorization = Literal["allowed", "blocked", "unknown"]

APP_OPERATION_PREVIEW_AUTHORIZATION_VALUES: set[AppOperationPreviewAuthorization] = {
    "allowed",
    "blocked",
    "unknown",
}


def check_app_operation_preview_authorization(value: str) -> AppOperationPreviewAuthorization:
    if value in APP_OPERATION_PREVIEW_AUTHORIZATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATION_PREVIEW_AUTHORIZATION_VALUES!r}")
