from typing import Literal

CreateAPIConsumerRateCardRequestUnit = Literal["compute_second", "request", "response_megabyte"]

CREATE_API_CONSUMER_RATE_CARD_REQUEST_UNIT_VALUES: set[CreateAPIConsumerRateCardRequestUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_create_api_consumer_rate_card_request_unit(value: str) -> CreateAPIConsumerRateCardRequestUnit:
    if value in CREATE_API_CONSUMER_RATE_CARD_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_API_CONSUMER_RATE_CARD_REQUEST_UNIT_VALUES!r}"
    )
