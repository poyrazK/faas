from typing import Literal

PreviewEventRequestDataContentType = Literal["application/json"]

PREVIEW_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES: set[PreviewEventRequestDataContentType] = {
    "application/json",
}


def check_preview_event_request_data_content_type(value: str) -> PreviewEventRequestDataContentType:
    if value in PREVIEW_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES!r}")
