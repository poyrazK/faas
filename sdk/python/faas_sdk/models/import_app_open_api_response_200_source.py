from typing import Literal

ImportAppOpenAPIResponse200Source = Literal["manual_import"]

IMPORT_APP_OPEN_API_RESPONSE_200_SOURCE_VALUES: set[ImportAppOpenAPIResponse200Source] = {
    "manual_import",
}


def check_import_app_open_api_response_200_source(value: str) -> ImportAppOpenAPIResponse200Source:
    if value in IMPORT_APP_OPEN_API_RESPONSE_200_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {IMPORT_APP_OPEN_API_RESPONSE_200_SOURCE_VALUES!r}")
