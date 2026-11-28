from typing import Literal

BindingApplicationAckTargetReloadReason = Literal[
    "binding_secret_unexpected",
    "current",
    "projection_failed",
    "reload_observation_missing",
    "reload_observation_time_invalid",
    "reload_outcome_unknown",
    "reload_stale",
    "reload_version_invalid",
    "signal_failed",
    "target_inconsistent",
]

BINDING_APPLICATION_ACK_TARGET_RELOAD_REASON_VALUES: set[BindingApplicationAckTargetReloadReason] = {
    "binding_secret_unexpected",
    "current",
    "projection_failed",
    "reload_observation_missing",
    "reload_observation_time_invalid",
    "reload_outcome_unknown",
    "reload_stale",
    "reload_version_invalid",
    "signal_failed",
    "target_inconsistent",
}


def check_binding_application_ack_target_reload_reason(value: str) -> BindingApplicationAckTargetReloadReason:
    if value in BINDING_APPLICATION_ACK_TARGET_RELOAD_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_RELOAD_REASON_VALUES!r}"
    )
