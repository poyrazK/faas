from typing import Literal

EventExecutionLineageRetryJobControlFiltersCredentialType = Literal[
    "", "api_key", "deploy_token", "internal", "platform_tenant_token", "session"
]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_CREDENTIAL_TYPE_VALUES: set[
    EventExecutionLineageRetryJobControlFiltersCredentialType
] = {
    "",
    "api_key",
    "deploy_token",
    "internal",
    "platform_tenant_token",
    "session",
}


def check_event_execution_lineage_retry_job_control_filters_credential_type(
    value: str,
) -> EventExecutionLineageRetryJobControlFiltersCredentialType:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_CREDENTIAL_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_CREDENTIAL_TYPE_VALUES!r}"
    )
