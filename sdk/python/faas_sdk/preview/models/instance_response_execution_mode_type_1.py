from typing import Literal

InstanceResponseExecutionModeType1 = Literal["job", "mirror", "normal", "service", "worker"]

INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_1_VALUES: set[InstanceResponseExecutionModeType1] = {
    "job",
    "mirror",
    "normal",
    "service",
    "worker",
}


def check_instance_response_execution_mode_type_1(value: str) -> InstanceResponseExecutionModeType1:
    if value in INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_1_VALUES!r}")
