from typing import Literal

BindingVerificationDiagnosticBlockerResourceKind = Literal["queue", "task"]

BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RESOURCE_KIND_VALUES: set[BindingVerificationDiagnosticBlockerResourceKind] = {
    "queue",
    "task",
}


def check_binding_verification_diagnostic_blocker_resource_kind(
    value: str,
) -> BindingVerificationDiagnosticBlockerResourceKind:
    if value in BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RESOURCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_DIAGNOSTIC_BLOCKER_RESOURCE_KIND_VALUES!r}"
    )
