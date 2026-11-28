from typing import Literal

OperatorRuntimeConfigOperationApplyMode = Literal["break_glass", "graceful", "rolling"]

OPERATOR_RUNTIME_CONFIG_OPERATION_APPLY_MODE_VALUES: set[OperatorRuntimeConfigOperationApplyMode] = {
    "break_glass",
    "graceful",
    "rolling",
}


def check_operator_runtime_config_operation_apply_mode(value: str) -> OperatorRuntimeConfigOperationApplyMode:
    if value in OPERATOR_RUNTIME_CONFIG_OPERATION_APPLY_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_OPERATION_APPLY_MODE_VALUES!r}"
    )
