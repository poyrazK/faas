from typing import Literal

ApplicationStandardLogDestinationKind = Literal["http_json", "otlp"]

APPLICATION_STANDARD_LOG_DESTINATION_KIND_VALUES: set[ApplicationStandardLogDestinationKind] = {
    "http_json",
    "otlp",
}


def check_application_standard_log_destination_kind(value: str) -> ApplicationStandardLogDestinationKind:
    if value in APPLICATION_STANDARD_LOG_DESTINATION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_LOG_DESTINATION_KIND_VALUES!r}")
