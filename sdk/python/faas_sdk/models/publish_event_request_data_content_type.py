from typing import Literal

PublishEventRequestDataContentType = Literal["application/json"]

PUBLISH_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES: set[PublishEventRequestDataContentType] = {
    "application/json",
}


def check_publish_event_request_data_content_type(value: str) -> PublishEventRequestDataContentType:
    if value in PUBLISH_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLISH_EVENT_REQUEST_DATA_CONTENT_TYPE_VALUES!r}")
