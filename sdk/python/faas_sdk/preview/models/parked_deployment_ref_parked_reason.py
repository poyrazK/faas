from typing import Literal

ParkedDeploymentRefParkedReason = Literal[
    "admin_park", "lifecycle_park", "liveness_exhausted", "security_scan_regressed"
]

PARKED_DEPLOYMENT_REF_PARKED_REASON_VALUES: set[ParkedDeploymentRefParkedReason] = {
    "admin_park",
    "lifecycle_park",
    "liveness_exhausted",
    "security_scan_regressed",
}


def check_parked_deployment_ref_parked_reason(value: str) -> ParkedDeploymentRefParkedReason:
    if value in PARKED_DEPLOYMENT_REF_PARKED_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PARKED_DEPLOYMENT_REF_PARKED_REASON_VALUES!r}")
