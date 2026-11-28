from typing import Literal

DeploymentResponseParkedReasonType1 = Literal[
    "admin_park", "lifecycle_park", "liveness_exhausted", "security_scan_regressed"
]

DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_1_VALUES: set[DeploymentResponseParkedReasonType1] = {
    "admin_park",
    "lifecycle_park",
    "liveness_exhausted",
    "security_scan_regressed",
}


def check_deployment_response_parked_reason_type_1(value: str) -> DeploymentResponseParkedReasonType1:
    if value in DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_1_VALUES!r}")
