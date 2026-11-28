from typing import Literal

DeletedAppPurgeResponsePendingReason = Literal[
    "environment_clone",
    "exclusive_work",
    "foreign_key",
    "managed_postgres_binding",
    "multipart_write",
    "object_bucket",
    "retained_resources",
    "retry_pending",
]

DELETED_APP_PURGE_RESPONSE_PENDING_REASON_VALUES: set[DeletedAppPurgeResponsePendingReason] = {
    "environment_clone",
    "exclusive_work",
    "foreign_key",
    "managed_postgres_binding",
    "multipart_write",
    "object_bucket",
    "retained_resources",
    "retry_pending",
}


def check_deleted_app_purge_response_pending_reason(value: str) -> DeletedAppPurgeResponsePendingReason:
    if value in DELETED_APP_PURGE_RESPONSE_PENDING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELETED_APP_PURGE_RESPONSE_PENDING_REASON_VALUES!r}")
