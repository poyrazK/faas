from typing import Literal

DebugTelemetryRequestItemMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

DEBUG_TELEMETRY_REQUEST_ITEM_METHOD_VALUES: set[DebugTelemetryRequestItemMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_debug_telemetry_request_item_method(value: str) -> DebugTelemetryRequestItemMethod:
    if value in DEBUG_TELEMETRY_REQUEST_ITEM_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_TELEMETRY_REQUEST_ITEM_METHOD_VALUES!r}")
