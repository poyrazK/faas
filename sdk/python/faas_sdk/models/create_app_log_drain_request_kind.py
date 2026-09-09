from typing import Literal

CreateAppLogDrainRequestKind = Literal["http_json", "otlp"]

CREATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES: set[CreateAppLogDrainRequestKind] = {
    "http_json",
    "otlp",
}


def check_create_app_log_drain_request_kind(value: str) -> CreateAppLogDrainRequestKind:
    if value in CREATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_LOG_DRAIN_REQUEST_KIND_VALUES!r}")
