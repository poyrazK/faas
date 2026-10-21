from typing import Literal

EventExecutionLineageRetryJobControlReceiptCredentialType = Literal[
    "api_key", "deploy_token", "internal", "platform_tenant_token", "session"
]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_CREDENTIAL_TYPE_VALUES: set[
    EventExecutionLineageRetryJobControlReceiptCredentialType
] = {
    "api_key",
    "deploy_token",
    "internal",
    "platform_tenant_token",
    "session",
}


def check_event_execution_lineage_retry_job_control_receipt_credential_type(
    value: str,
) -> EventExecutionLineageRetryJobControlReceiptCredentialType:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_CREDENTIAL_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_CREDENTIAL_TYPE_VALUES!r}"
    )
