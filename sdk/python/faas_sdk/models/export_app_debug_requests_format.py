from typing import Literal

ExportAppDebugRequestsFormat = Literal["csv", "ndjson"]

EXPORT_APP_DEBUG_REQUESTS_FORMAT_VALUES: set[ExportAppDebugRequestsFormat] = {
    "csv",
    "ndjson",
}


def check_export_app_debug_requests_format(value: str) -> ExportAppDebugRequestsFormat:
    if value in EXPORT_APP_DEBUG_REQUESTS_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXPORT_APP_DEBUG_REQUESTS_FORMAT_VALUES!r}")
