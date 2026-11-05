from typing import Literal

BindingApplicationAckTargetApplicationAckStatus = Literal["current", "failed", "stale", "unknown"]

BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_STATUS_VALUES: set[BindingApplicationAckTargetApplicationAckStatus] = {
    "current",
    "failed",
    "stale",
    "unknown",
}


def check_binding_application_ack_target_application_ack_status(
    value: str,
) -> BindingApplicationAckTargetApplicationAckStatus:
    if value in BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_STATUS_VALUES!r}"
    )
