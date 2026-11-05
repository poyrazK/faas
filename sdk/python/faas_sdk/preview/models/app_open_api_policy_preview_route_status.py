from typing import Literal

AppOpenAPIPolicyPreviewRouteStatus = Literal["declared_only", "matched", "observed_only"]

APP_OPEN_API_POLICY_PREVIEW_ROUTE_STATUS_VALUES: set[AppOpenAPIPolicyPreviewRouteStatus] = {
    "declared_only",
    "matched",
    "observed_only",
}


def check_app_open_api_policy_preview_route_status(value: str) -> AppOpenAPIPolicyPreviewRouteStatus:
    if value in APP_OPEN_API_POLICY_PREVIEW_ROUTE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPEN_API_POLICY_PREVIEW_ROUTE_STATUS_VALUES!r}")
