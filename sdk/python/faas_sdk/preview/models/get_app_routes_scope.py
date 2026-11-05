from typing import Literal

GetAppRoutesScope = Literal["default"]

GET_APP_ROUTES_SCOPE_VALUES: set[GetAppRoutesScope] = {
    "default",
}


def check_get_app_routes_scope(value: str) -> GetAppRoutesScope:
    if value in GET_APP_ROUTES_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_ROUTES_SCOPE_VALUES!r}")
