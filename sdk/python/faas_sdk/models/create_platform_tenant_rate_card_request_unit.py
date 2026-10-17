from typing import Literal

CreatePlatformTenantRateCardRequestUnit = Literal["compute_second", "request", "response_megabyte"]

CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_UNIT_VALUES: set[CreatePlatformTenantRateCardRequestUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_create_platform_tenant_rate_card_request_unit(value: str) -> CreatePlatformTenantRateCardRequestUnit:
    if value in CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_RATE_CARD_REQUEST_UNIT_VALUES!r}"
    )
