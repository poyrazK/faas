from typing import Literal

CancelDeploymentRequestReason = Literal["auto_health", "auto_quota", "system", "user"]

CANCEL_DEPLOYMENT_REQUEST_REASON_VALUES: set[CancelDeploymentRequestReason] = {
    "auto_health",
    "auto_quota",
    "system",
    "user",
}


def check_cancel_deployment_request_reason(value: str) -> CancelDeploymentRequestReason:
    if value in CANCEL_DEPLOYMENT_REQUEST_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANCEL_DEPLOYMENT_REQUEST_REASON_VALUES!r}")
