from typing import Literal

AppResponsePreviewPrState = Literal["closed", "open", "stale", "torn_down"]

APP_RESPONSE_PREVIEW_PR_STATE_VALUES: set[AppResponsePreviewPrState] = {
    "closed",
    "open",
    "stale",
    "torn_down",
}


def check_app_response_preview_pr_state(value: str) -> AppResponsePreviewPrState:
    if value in APP_RESPONSE_PREVIEW_PR_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_PREVIEW_PR_STATE_VALUES!r}")
