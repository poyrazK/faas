from typing import Literal

BindingVerificationDiagnosticBlockerRecovery = Literal[
    "cancel", "resume", "retry_diagnostics", "review_new_run", "wait", "wait_for_application_ack"
]

BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RECOVERY_VALUES: set[BindingVerificationDiagnosticBlockerRecovery] = {
    "cancel",
    "resume",
    "retry_diagnostics",
    "review_new_run",
    "wait",
    "wait_for_application_ack",
}


def check_binding_verification_diagnostic_blocker_recovery(value: str) -> BindingVerificationDiagnosticBlockerRecovery:
    if value in BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RECOVERY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RECOVERY_VALUES!r}"
    )
