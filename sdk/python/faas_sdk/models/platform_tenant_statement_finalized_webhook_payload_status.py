from typing import Literal

PlatformTenantStatementFinalizedWebhookPayloadStatus = Literal["finalized"]

PLATFORM_TENANT_STATEMENT_FINALIZED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[
    PlatformTenantStatementFinalizedWebhookPayloadStatus
] = {
    "finalized",
}


def check_platform_tenant_statement_finalized_webhook_payload_status(
    value: str,
) -> PlatformTenantStatementFinalizedWebhookPayloadStatus:
    if value in PLATFORM_TENANT_STATEMENT_FINALIZED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_STATEMENT_FINALIZED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}"
    )
