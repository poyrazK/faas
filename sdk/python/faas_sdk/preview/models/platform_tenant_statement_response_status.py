from typing import Literal

PlatformTenantStatementResponseStatus = Literal["draft", "finalized", "superseded"]

PLATFORM_TENANT_STATEMENT_RESPONSE_STATUS_VALUES: set[PlatformTenantStatementResponseStatus] = {
    "draft",
    "finalized",
    "superseded",
}


def check_platform_tenant_statement_response_status(value: str) -> PlatformTenantStatementResponseStatus:
    if value in PLATFORM_TENANT_STATEMENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_STATEMENT_RESPONSE_STATUS_VALUES!r}")
