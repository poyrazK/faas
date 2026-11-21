from typing import Literal

ExecutionClassCapabilityExecutionClass = Literal["extended", "standard"]

EXECUTION_CLASS_CAPABILITY_EXECUTION_CLASS_VALUES: set[ExecutionClassCapabilityExecutionClass] = {
    "extended",
    "standard",
}


def check_execution_class_capability_execution_class(value: str) -> ExecutionClassCapabilityExecutionClass:
    if value in EXECUTION_CLASS_CAPABILITY_EXECUTION_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_CLASS_CAPABILITY_EXECUTION_CLASS_VALUES!r}"
    )
