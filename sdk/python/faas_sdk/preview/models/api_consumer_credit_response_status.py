from typing import Literal

APIConsumerCreditResponseStatus = Literal["active", "expired", "used", "voided"]

API_CONSUMER_CREDIT_RESPONSE_STATUS_VALUES: set[APIConsumerCreditResponseStatus] = {
    "active",
    "expired",
    "used",
    "voided",
}


def check_api_consumer_credit_response_status(value: str) -> APIConsumerCreditResponseStatus:
    if value in API_CONSUMER_CREDIT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_CREDIT_RESPONSE_STATUS_VALUES!r}")
