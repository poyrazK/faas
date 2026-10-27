from typing import Literal

AlertRollbackDeploymentEvidenceStatus = Literal[
    "breached", "expired", "healthy", "insufficient", "pending", "stale", "unavailable", "unsupported"
]

ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_STATUS_VALUES: set[AlertRollbackDeploymentEvidenceStatus] = {
    "breached",
    "expired",
    "healthy",
    "insufficient",
    "pending",
    "stale",
    "unavailable",
    "unsupported",
}


def check_alert_rollback_deployment_evidence_status(value: str) -> AlertRollbackDeploymentEvidenceStatus:
    if value in ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_STATUS_VALUES!r}")
