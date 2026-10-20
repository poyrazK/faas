from typing import Literal

AlertRollbackDeploymentEvidenceVersion = Literal[1]

ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_VERSION_VALUES: set[AlertRollbackDeploymentEvidenceVersion] = {
    1,
}


def check_alert_rollback_deployment_evidence_version(value: int) -> AlertRollbackDeploymentEvidenceVersion:
    if value in ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_ROLLBACK_DEPLOYMENT_EVIDENCE_VERSION_VALUES!r}"
    )
