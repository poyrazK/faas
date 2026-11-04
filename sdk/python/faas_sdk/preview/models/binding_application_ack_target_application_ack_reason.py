from typing import Literal

BindingApplicationAckTargetApplicationAckReason = Literal[
    "application_ack_failed",
    "application_ack_generation_mismatch",
    "application_ack_generation_missing",
    "application_ack_missing",
    "application_ack_outcome_unknown",
    "application_ack_stale",
    "application_ack_time_invalid",
    "application_ack_version_invalid",
    "binding_secret_unexpected",
    "current",
    "process_generation_invalid",
    "process_generation_missing",
    "reload_disabled",
    "reload_support_unknown",
    "target_inconsistent",
]

BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_REASON_VALUES: set[BindingApplicationAckTargetApplicationAckReason] = {
    "application_ack_failed",
    "application_ack_generation_mismatch",
    "application_ack_generation_missing",
    "application_ack_missing",
    "application_ack_outcome_unknown",
    "application_ack_stale",
    "application_ack_time_invalid",
    "application_ack_version_invalid",
    "binding_secret_unexpected",
    "current",
    "process_generation_invalid",
    "process_generation_missing",
    "reload_disabled",
    "reload_support_unknown",
    "target_inconsistent",
}


def check_binding_application_ack_target_application_ack_reason(
    value: str,
) -> BindingApplicationAckTargetApplicationAckReason:
    if value in BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ACK_TARGET_APPLICATION_ACK_REASON_VALUES!r}"
    )
