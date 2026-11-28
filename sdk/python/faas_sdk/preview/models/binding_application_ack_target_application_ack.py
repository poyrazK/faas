from typing import Literal

BindingApplicationAckTargetApplicationAck = Literal["applied", "failed"]

BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_VALUES: set[BindingApplicationAckTargetApplicationAck] = {
    "applied",
    "failed",
}


def check_binding_application_ack_target_application_ack(value: str) -> BindingApplicationAckTargetApplicationAck:
    if value in BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_VALUES!r}"
    )
