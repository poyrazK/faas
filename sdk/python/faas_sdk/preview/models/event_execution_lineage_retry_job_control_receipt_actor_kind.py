from typing import Literal

EventExecutionLineageRetryJobControlReceiptActorKind = Literal["account", "credential", "internal"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTOR_KIND_VALUES: set[
    EventExecutionLineageRetryJobControlReceiptActorKind
] = {
    "account",
    "credential",
    "internal",
}


def check_event_execution_lineage_retry_job_control_receipt_actor_kind(
    value: str,
) -> EventExecutionLineageRetryJobControlReceiptActorKind:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTOR_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTOR_KIND_VALUES!r}"
    )
