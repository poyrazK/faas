from typing import Literal

SelectedTraceLookupResponseInvocationsStatus = Literal["ok", "unavailable"]

SELECTED_TRACE_LOOKUP_RESPONSE_INVOCATIONS_STATUS_VALUES: set[SelectedTraceLookupResponseInvocationsStatus] = {
    "ok",
    "unavailable",
}


def check_selected_trace_lookup_response_invocations_status(value: str) -> SelectedTraceLookupResponseInvocationsStatus:
    if value in SELECTED_TRACE_LOOKUP_RESPONSE_INVOCATIONS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SELECTED_TRACE_LOOKUP_RESPONSE_INVOCATIONS_STATUS_VALUES!r}"
    )
