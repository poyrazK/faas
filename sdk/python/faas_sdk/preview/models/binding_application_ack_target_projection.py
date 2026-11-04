from typing import Literal

BindingApplicationAckTargetProjection = Literal["failed", "unchanged", "updated"]

BINDING_APPLICATION_ACK_TARGET_PROJECTION_VALUES: set[BindingApplicationAckTargetProjection] = {
    "failed",
    "unchanged",
    "updated",
}


def check_binding_application_ack_target_projection(value: str) -> BindingApplicationAckTargetProjection:
    if value in BINDING_APPLICATION_ACK_TARGET_PROJECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_PROJECTION_VALUES!r}")
