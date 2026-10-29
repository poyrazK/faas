from typing import Literal

GetAppOpenAPISource = Literal["auto", "manual_import"]

GET_APP_OPEN_API_SOURCE_VALUES: set[GetAppOpenAPISource] = {
    "auto",
    "manual_import",
}


def check_get_app_open_api_source(value: str) -> GetAppOpenAPISource:
    if value in GET_APP_OPEN_API_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_OPEN_API_SOURCE_VALUES!r}")
