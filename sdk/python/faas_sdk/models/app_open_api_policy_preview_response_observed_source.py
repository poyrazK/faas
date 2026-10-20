from typing import Literal

AppOpenAPIPolicyPreviewResponseObservedSource = Literal["live", "partial", "unavailable"]

APP_OPEN_API_POLICY_PREVIEW_RESPONSE_OBSERVED_SOURCE_VALUES: set[AppOpenAPIPolicyPreviewResponseObservedSource] = {
    "live",
    "partial",
    "unavailable",
}


def check_app_open_api_policy_preview_response_observed_source(
    value: str,
) -> AppOpenAPIPolicyPreviewResponseObservedSource:
    if value in APP_OPEN_API_POLICY_PREVIEW_RESPONSE_OBSERVED_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_OPEN_API_POLICY_PREVIEW_RESPONSE_OBSERVED_SOURCE_VALUES!r}"
    )
