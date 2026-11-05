from typing import Literal

WorkflowRecoveryInitialQueueEvidenceStage = Literal["prepared", "submitted"]

WORKFLOW_RECOVERY_INITIAL_QUEUE_EVIDENCE_STAGE_VALUES: set[WorkflowRecoveryInitialQueueEvidenceStage] = {
    "prepared",
    "submitted",
}


def check_workflow_recovery_initial_queue_evidence_stage(value: str) -> WorkflowRecoveryInitialQueueEvidenceStage:
    if value in WORKFLOW_RECOVERY_INITIAL_QUEUE_EVIDENCE_STAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_INITIAL_QUEUE_EVIDENCE_STAGE_VALUES!r}"
    )
