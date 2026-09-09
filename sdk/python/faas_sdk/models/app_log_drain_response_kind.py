from typing import Literal

AppLogDrainResponseKind = Literal["http_json", "otlp"]

APP_LOG_DRAIN_RESPONSE_KIND_VALUES: set[AppLogDrainResponseKind] = {
    "http_json",
    "otlp",
}


def check_app_log_drain_response_kind(value: str) -> AppLogDrainResponseKind:
    if value in APP_LOG_DRAIN_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_LOG_DRAIN_RESPONSE_KIND_VALUES!r}")
