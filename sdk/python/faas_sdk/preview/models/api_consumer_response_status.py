from typing import Literal

APIConsumerResponseStatus = Literal["active", "revoked"]

API_CONSUMER_RESPONSE_STATUS_VALUES: set[APIConsumerResponseStatus] = {
    "active",
    "revoked",
}


def check_api_consumer_response_status(value: str) -> APIConsumerResponseStatus:
    if value in API_CONSUMER_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_RESPONSE_STATUS_VALUES!r}")
