from typing import Literal

HostedTaskBindingVerificationStatus = Literal[
    "expired", "failed", "passed", "revoked", "selected", "stale", "verifying"
]

HOSTED_TASK_BINDING_VERIFICATION_STATUS_VALUES: set[HostedTaskBindingVerificationStatus] = {
    "expired",
    "failed",
    "passed",
    "revoked",
    "selected",
    "stale",
    "verifying",
}


def check_hosted_task_binding_verification_status(value: str) -> HostedTaskBindingVerificationStatus:
    if value in HOSTED_TASK_BINDING_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_BINDING_VERIFICATION_STATUS_VALUES!r}")
