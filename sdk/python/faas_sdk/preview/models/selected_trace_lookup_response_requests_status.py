from typing import Literal

SelectedTraceLookupResponseRequestsStatus = Literal["ok", "unavailable"]

SELECTED_TRACE_LOOKUP_RESPONSE_REQUESTS_STATUS_VALUES: set[SelectedTraceLookupResponseRequestsStatus] = {
    "ok",
    "unavailable",
}


def check_selected_trace_lookup_response_requests_status(value: str) -> SelectedTraceLookupResponseRequestsStatus:
    if value in SELECTED_TRACE_LOOKUP_RESPONSE_REQUESTS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SELECTED_TRACE_LOOKUP_RESPONSE_REQUESTS_STATUS_VALUES!r}"
    )
