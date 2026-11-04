from typing import Literal

AppOperationPreviewSchemaVersion = Literal[1]

APP_OPERATION_PREVIEW_SCHEMA_VERSION_VALUES: set[AppOperationPreviewSchemaVersion] = {
    1,
}


def check_app_operation_preview_schema_version(value: int) -> AppOperationPreviewSchemaVersion:
    if value in APP_OPERATION_PREVIEW_SCHEMA_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATION_PREVIEW_SCHEMA_VERSION_VALUES!r}")
