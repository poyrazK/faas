from typing import Literal

BindingVerificationRunDiagnosticsStatus = Literal["blocked", "cancelled", "expired", "passed", "pending"]

BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES: set[BindingVerificationRunDiagnosticsStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_binding_verification_run_diagnostics_status(value: str) -> BindingVerificationRunDiagnosticsStatus:
    if value in BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES!r}"
    )
