from typing import Literal

AppOpenAPIImportResponseOpenapiVersion = Literal["3.0.0", "3.0.1", "3.0.2", "3.0.3", "3.0.4", "3.1.0", "3.1.1"]

APP_OPEN_API_IMPORT_RESPONSE_OPENAPI_VERSION_VALUES: set[AppOpenAPIImportResponseOpenapiVersion] = {
    "3.0.0",
    "3.0.1",
    "3.0.2",
    "3.0.3",
    "3.0.4",
    "3.1.0",
    "3.1.1",
}


def check_app_open_api_import_response_openapi_version(value: str) -> AppOpenAPIImportResponseOpenapiVersion:
    if value in APP_OPEN_API_IMPORT_RESPONSE_OPENAPI_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_OPEN_API_IMPORT_RESPONSE_OPENAPI_VERSION_VALUES!r}"
    )
