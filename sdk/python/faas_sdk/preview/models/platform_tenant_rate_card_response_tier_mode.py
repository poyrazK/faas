from typing import Literal

PlatformTenantRateCardResponseTierMode = Literal["graduated", "volume"]

PLATFORM_TENANT_RATE_CARD_RESPONSE_TIER_MODE_VALUES: set[PlatformTenantRateCardResponseTierMode] = {
    "graduated",
    "volume",
}


def check_platform_tenant_rate_card_response_tier_mode(value: str) -> PlatformTenantRateCardResponseTierMode:
    if value in PLATFORM_TENANT_RATE_CARD_RESPONSE_TIER_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_RATE_CARD_RESPONSE_TIER_MODE_VALUES!r}"
    )
