from typing import Literal

ExecutionPipelineFinishedWebhookPayloadOutcome = Literal["cancelled", "deadline_exceeded", "failed", "succeeded"]

EXECUTION_PIPELINE_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES: set[ExecutionPipelineFinishedWebhookPayloadOutcome] = {
    "cancelled",
    "deadline_exceeded",
    "failed",
    "succeeded",
}


def check_execution_pipeline_finished_webhook_payload_outcome(
    value: str,
) -> ExecutionPipelineFinishedWebhookPayloadOutcome:
    if value in EXECUTION_PIPELINE_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES!r}"
    )
