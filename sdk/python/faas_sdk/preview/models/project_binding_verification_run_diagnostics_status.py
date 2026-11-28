from typing import Literal

ProjectBindingVerificationRunDiagnosticsStatus = Literal["blocked", "cancelled", "expired", "passed", "pending"]

PROJECT_BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES: set[ProjectBindingVerificationRunDiagnosticsStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_project_binding_verification_run_diagnostics_status(
    value: str,
) -> ProjectBindingVerificationRunDiagnosticsStatus:
    if value in PROJECT_BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_BINDING_VERIFICATION_RUN_DIAGNOSTICS_STATUS_VALUES!r}"
    )
