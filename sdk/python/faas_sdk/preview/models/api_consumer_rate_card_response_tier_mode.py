from typing import Literal

APIConsumerRateCardResponseTierMode = Literal["graduated", "volume"]

API_CONSUMER_RATE_CARD_RESPONSE_TIER_MODE_VALUES: set[APIConsumerRateCardResponseTierMode] = {
    "graduated",
    "volume",
}


def check_api_consumer_rate_card_response_tier_mode(value: str) -> APIConsumerRateCardResponseTierMode:
    if value in API_CONSUMER_RATE_CARD_RESPONSE_TIER_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_RATE_CARD_RESPONSE_TIER_MODE_VALUES!r}")
