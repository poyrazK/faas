from typing import Literal

BindingVerificationRecoveryActionAction = Literal[
    "cancel", "resume", "retry_diagnostics", "review_new_run", "wait", "wait_for_application_ack"
]

BINDING_VERIFICATION_RECOVERY_ACTION_ACTION_VALUES: set[BindingVerificationRecoveryActionAction] = {
    "cancel",
    "resume",
    "retry_diagnostics",
    "review_new_run",
    "wait",
    "wait_for_application_ack",
}


def check_binding_verification_recovery_action_action(value: str) -> BindingVerificationRecoveryActionAction:
    if value in BINDING_VERIFICATION_RECOVERY_ACTION_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_RECOVERY_ACTION_ACTION_VALUES!r}"
    )
