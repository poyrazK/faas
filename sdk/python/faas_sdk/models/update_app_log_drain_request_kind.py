from typing import Literal

UpdateAppLogDrainRequestKind = Literal["http_json", "otlp"]

UPDATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES: set[UpdateAppLogDrainRequestKind] = {
    "http_json",
    "otlp",
}


def check_update_app_log_drain_request_kind(value: str) -> UpdateAppLogDrainRequestKind:
    if value in UPDATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES!r}")
