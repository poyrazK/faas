from typing import Literal

BindingApplicationAckTargetReloadStatus = Literal["current", "failed", "stale", "unknown"]

BINDING_APPLICATION_ACK_TARGET_RELOAD_STATUS_VALUES: set[BindingApplicationAckTargetReloadStatus] = {
    "current",
    "failed",
    "stale",
    "unknown",
}


def check_binding_application_ack_target_reload_status(value: str) -> BindingApplicationAckTargetReloadStatus:
    if value in BINDING_APPLICATION_ACK_TARGET_RELOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_RELOAD_STATUS_VALUES!r}"
    )
