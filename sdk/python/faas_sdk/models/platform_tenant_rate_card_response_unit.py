from typing import Literal

PlatformTenantRateCardResponseUnit = Literal["compute_second", "request", "response_megabyte"]

PLATFORM_TENANT_RATE_CARD_RESPONSE_UNIT_VALUES: set[PlatformTenantRateCardResponseUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_platform_tenant_rate_card_response_unit(value: str) -> PlatformTenantRateCardResponseUnit:
    if value in PLATFORM_TENANT_RATE_CARD_RESPONSE_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_RATE_CARD_RESPONSE_UNIT_VALUES!r}")
