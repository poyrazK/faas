from typing import Literal

CreateAPIConsumerRateCardRequestTierMode = Literal["graduated", "volume"]

CREATE_API_CONSUMER_RATE_CARD_REQUEST_TIER_MODE_VALUES: set[CreateAPIConsumerRateCardRequestTierMode] = {
    "graduated",
    "volume",
}


def check_create_api_consumer_rate_card_request_tier_mode(value: str) -> CreateAPIConsumerRateCardRequestTierMode:
    if value in CREATE_API_CONSUMER_RATE_CARD_REQUEST_TIER_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_API_CONSUMER_RATE_CARD_REQUEST_TIER_MODE_VALUES!r}"
    )
