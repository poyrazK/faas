from typing import Literal

PreviewEventRequestDatacontenttype = Literal["application/json"]

PREVIEW_EVENT_REQUEST_DATACONTENTTYPE_VALUES: set[PreviewEventRequestDatacontenttype] = {
    "application/json",
}


def check_preview_event_request_datacontenttype(value: str) -> PreviewEventRequestDatacontenttype:
    if value in PREVIEW_EVENT_REQUEST_DATACONTENTTYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_EVENT_REQUEST_DATACONTENTTYPE_VALUES!r}")
