from typing import Literal

ListEventExecutionLineageRetryJobControlsCredentialType = Literal[
    "", "api_key", "deploy_token", "internal", "platform_tenant_token", "session"
]

LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_CREDENTIAL_TYPE_VALUES: set[
    ListEventExecutionLineageRetryJobControlsCredentialType
] = {
    "",
    "api_key",
    "deploy_token",
    "internal",
    "platform_tenant_token",
    "session",
}


def check_list_event_execution_lineage_retry_job_controls_credential_type(
    value: str,
) -> ListEventExecutionLineageRetryJobControlsCredentialType:
    if value in LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_CREDENTIAL_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_CREDENTIAL_TYPE_VALUES!r}"
    )
