from typing import Literal

BindingApplicationAckTargetSignal = Literal["failed", "not_attempted", "queued", "sent"]

BINDING_APPLICATION_ACK_TARGET_SIGNAL_VALUES: set[BindingApplicationAckTargetSignal] = {
    "failed",
    "not_attempted",
    "queued",
    "sent",
}


def check_binding_application_ack_target_signal(value: str) -> BindingApplicationAckTargetSignal:
    if value in BINDING_APPLICATION_ACK_TARGET_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_SIGNAL_VALUES!r}")
