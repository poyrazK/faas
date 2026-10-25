from typing import Literal

PublishEventRequestDatacontenttype = Literal["application/json"]

PUBLISH_EVENT_REQUEST_DATACONTENTTYPE_VALUES: set[PublishEventRequestDatacontenttype] = {
    "application/json",
}


def check_publish_event_request_datacontenttype(value: str) -> PublishEventRequestDatacontenttype:
    if value in PUBLISH_EVENT_REQUEST_DATACONTENTTYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLISH_EVENT_REQUEST_DATACONTENTTYPE_VALUES!r}")
