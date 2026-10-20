from typing import Literal

AppOpenAPIImportResponseSource = Literal["manual_import"]

APP_OPEN_API_IMPORT_RESPONSE_SOURCE_VALUES: set[AppOpenAPIImportResponseSource] = {
    "manual_import",
}


def check_app_open_api_import_response_source(value: str) -> AppOpenAPIImportResponseSource:
    if value in APP_OPEN_API_IMPORT_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPEN_API_IMPORT_RESPONSE_SOURCE_VALUES!r}")
