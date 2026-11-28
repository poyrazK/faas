from typing import Literal

CreateApplicationStandardLogDestinationRequestKind = Literal["http_json", "otlp"]

CREATE_APPLICATION_STANDARD_LOG_DESTINATION_REQUEST_KIND_VALUES: set[
    CreateApplicationStandardLogDestinationRequestKind
] = {
    "http_json",
    "otlp",
}


def check_create_application_standard_log_destination_request_kind(
    value: str,
) -> CreateApplicationStandardLogDestinationRequestKind:
    if value in CREATE_APPLICATION_STANDARD_LOG_DESTINATION_REQUEST_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_APPLICATION_STANDARD_LOG_DESTINATION_REQUEST_KIND_VALUES!r}"
    )
