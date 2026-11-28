from typing import Literal

AppOpenAPIPolicyPreviewRouteMethod = Literal["delete", "get", "head", "options", "patch", "post", "put", "trace"]

APP_OPEN_API_POLICY_PREVIEW_ROUTE_METHOD_VALUES: set[AppOpenAPIPolicyPreviewRouteMethod] = {
    "delete",
    "get",
    "head",
    "options",
    "patch",
    "post",
    "put",
    "trace",
}


def check_app_open_api_policy_preview_route_method(value: str) -> AppOpenAPIPolicyPreviewRouteMethod:
    if value in APP_OPEN_API_POLICY_PREVIEW_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPEN_API_POLICY_PREVIEW_ROUTE_METHOD_VALUES!r}")
