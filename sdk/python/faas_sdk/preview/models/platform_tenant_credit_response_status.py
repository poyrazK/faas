from typing import Literal

PlatformTenantCreditResponseStatus = Literal["active", "expired", "used", "voided"]

PLATFORM_TENANT_CREDIT_RESPONSE_STATUS_VALUES: set[PlatformTenantCreditResponseStatus] = {
    "active",
    "expired",
    "used",
    "voided",
}


def check_platform_tenant_credit_response_status(value: str) -> PlatformTenantCreditResponseStatus:
    if value in PLATFORM_TENANT_CREDIT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CREDIT_RESPONSE_STATUS_VALUES!r}")
