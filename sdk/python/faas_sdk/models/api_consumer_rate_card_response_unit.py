from typing import Literal

APIConsumerRateCardResponseUnit = Literal["request"]

API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES: set[APIConsumerRateCardResponseUnit] = {
    "request",
}


def check_api_consumer_rate_card_response_unit(value: str) -> APIConsumerRateCardResponseUnit:
    if value in API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES!r}")
