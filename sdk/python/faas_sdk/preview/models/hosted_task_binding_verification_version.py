from typing import Literal

HostedTaskBindingVerificationVersion = Literal[1]

HOSTED_TASK_BINDING_VERIFICATION_VERSION_VALUES: set[HostedTaskBindingVerificationVersion] = {
    1,
}


def check_hosted_task_binding_verification_version(value: int) -> HostedTaskBindingVerificationVersion:
    if value in HOSTED_TASK_BINDING_VERIFICATION_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_BINDING_VERIFICATION_VERSION_VALUES!r}")
