from typing import Literal

InstanceResponseLifecycleFailureReasonType1 = Literal[
    "clean_exit", "crash_loop", "error_exit", "liveness_fail", "oom", "readiness_fail", "startup_fail"
]

INSTANCE_RESPONSE_LIFECYCLE_FAILURE_REASON_TYPE_1_VALUES: set[InstanceResponseLifecycleFailureReasonType1] = {
    "clean_exit",
    "crash_loop",
    "error_exit",
    "liveness_fail",
    "oom",
    "readiness_fail",
    "startup_fail",
}


def check_instance_response_lifecycle_failure_reason_type_1(value: str) -> InstanceResponseLifecycleFailureReasonType1:
    if value in INSTANCE_RESPONSE_LIFECYCLE_FAILURE_REASON_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {INSTANCE_RESPONSE_LIFECYCLE_FAILURE_REASON_TYPE_1_VALUES!r}"
    )
