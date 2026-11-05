from typing import Literal

InstanceResponseExecutionModeType2Type1 = Literal["job", "mirror", "normal", "service", "worker"]

INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES: set[InstanceResponseExecutionModeType2Type1] = {
    "job",
    "mirror",
    "normal",
    "service",
    "worker",
}


def check_instance_response_execution_mode_type_2_type_1(value: str) -> InstanceResponseExecutionModeType2Type1:
    if value in INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {INSTANCE_RESPONSE_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES!r}"
    )
