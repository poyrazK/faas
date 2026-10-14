from typing import Literal

APIConsumerRateCardResponseUnit = Literal["compute_second", "request", "response_megabyte"]

API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES: set[APIConsumerRateCardResponseUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_api_consumer_rate_card_response_unit(value: str) -> APIConsumerRateCardResponseUnit:
    if value in API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_RATE_CARD_RESPONSE_UNIT_VALUES!r}")
