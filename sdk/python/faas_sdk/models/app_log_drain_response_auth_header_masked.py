from typing import Literal

AppLogDrainResponseAuthHeaderMasked = Literal["", "***"]

APP_LOG_DRAIN_RESPONSE_AUTH_HEADER_MASKED_VALUES: set[AppLogDrainResponseAuthHeaderMasked] = {
    "",
    "***",
}


def check_app_log_drain_response_auth_header_masked(value: str) -> AppLogDrainResponseAuthHeaderMasked:
    if value in APP_LOG_DRAIN_RESPONSE_AUTH_HEADER_MASKED_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_LOG_DRAIN_RESPONSE_AUTH_HEADER_MASKED_VALUES!r}")
