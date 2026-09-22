from typing import Literal

DeploymentResponseParkedReasonType2Type1 = Literal[
    "admin_park", "lifecycle_park", "liveness_exhausted", "security_scan_regressed"
]

DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_2_TYPE_1_VALUES: set[DeploymentResponseParkedReasonType2Type1] = {
    "admin_park",
    "lifecycle_park",
    "liveness_exhausted",
    "security_scan_regressed",
}


def check_deployment_response_parked_reason_type_2_type_1(value: str) -> DeploymentResponseParkedReasonType2Type1:
    if value in DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_PARKED_REASON_TYPE_2_TYPE_1_VALUES!r}"
    )
