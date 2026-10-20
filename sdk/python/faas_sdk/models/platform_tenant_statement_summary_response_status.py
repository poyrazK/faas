from typing import Literal

PlatformTenantStatementSummaryResponseStatus = Literal["finalized"]

PLATFORM_TENANT_STATEMENT_SUMMARY_RESPONSE_STATUS_VALUES: set[PlatformTenantStatementSummaryResponseStatus] = {
    "finalized",
}


def check_platform_tenant_statement_summary_response_status(value: str) -> PlatformTenantStatementSummaryResponseStatus:
    if value in PLATFORM_TENANT_STATEMENT_SUMMARY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_STATEMENT_SUMMARY_RESPONSE_STATUS_VALUES!r}"
    )
