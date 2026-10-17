from typing import Literal

BindingVerificationRunStepResourceKind = Literal["queue", "task"]

BINDING_VERIFICATION_RUN_STEP_RESOURCE_KIND_VALUES: set[BindingVerificationRunStepResourceKind] = {
    "queue",
    "task",
}


def check_binding_verification_run_step_resource_kind(value: str) -> BindingVerificationRunStepResourceKind:
    if value in BINDING_VERIFICATION_RUN_STEP_RESOURCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_RUN_STEP_RESOURCE_KIND_VALUES!r}"
    )
