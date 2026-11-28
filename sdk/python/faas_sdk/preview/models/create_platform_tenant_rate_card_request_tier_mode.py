from typing import Literal

CreatePlatformTenantRateCardRequestTierMode = Literal["graduated", "volume"]

CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_TIER_MODE_VALUES: set[CreatePlatformTenantRateCardRequestTierMode] = {
    "graduated",
    "volume",
}


def check_create_platform_tenant_rate_card_request_tier_mode(value: str) -> CreatePlatformTenantRateCardRequestTierMode:
    if value in CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_TIER_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_TIER_MODE_VALUES!r}"
    )
