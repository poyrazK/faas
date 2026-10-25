from typing import Literal

SelectedTraceLookupResponseSelection = Literal["explicit_app_ids_v1"]

SELECTED_TRACE_LOOKUP_RESPONSE_SELECTION_VALUES: set[SelectedTraceLookupResponseSelection] = {
    "explicit_app_ids_v1",
}


def check_selected_trace_lookup_response_selection(value: str) -> SelectedTraceLookupResponseSelection:
    if value in SELECTED_TRACE_LOOKUP_RESPONSE_SELECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SELECTED_TRACE_LOOKUP_RESPONSE_SELECTION_VALUES!r}")
