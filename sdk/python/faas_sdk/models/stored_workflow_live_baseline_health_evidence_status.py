from typing import Literal

StoredWorkflowLiveBaselineHealthEvidenceStatus = Literal["failed", "unavailable", "usable"]

STORED_WORKFLOW_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES: set[StoredWorkflowLiveBaselineHealthEvidenceStatus] = {
    "failed",
    "unavailable",
    "usable",
}


def check_stored_workflow_live_baseline_health_evidence_status(
    value: str,
) -> StoredWorkflowLiveBaselineHealthEvidenceStatus:
    if value in STORED_WORKFLOW_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES!r}"
    )
