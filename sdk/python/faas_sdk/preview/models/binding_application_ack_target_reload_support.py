from typing import Literal

BindingApplicationAckTargetReloadSupport = Literal["disabled", "enabled", "unknown"]

BINDING_APPLICATION_ACK_TARGET_RELOAD_SUPPORT_VALUES: set[BindingApplicationAckTargetReloadSupport] = {
    "disabled",
    "enabled",
    "unknown",
}


def check_binding_application_ack_target_reload_support(value: str) -> BindingApplicationAckTargetReloadSupport:
    if value in BINDING_APPLICATION_ACK_TARGET_RELOAD_SUPPORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_RELOAD_SUPPORT_VALUES!r}"
    )
