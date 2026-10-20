from typing import Literal

OperatorRuntimeConfigApplyMode = Literal["break_glass", "graceful", "hot", "rolling"]

OPERATOR_RUNTIME_CONFIG_APPLY_MODE_VALUES: set[OperatorRuntimeConfigApplyMode] = {
    "break_glass",
    "graceful",
    "hot",
    "rolling",
}


def check_operator_runtime_config_apply_mode(value: str) -> OperatorRuntimeConfigApplyMode:
    if value in OPERATOR_RUNTIME_CONFIG_APPLY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_APPLY_MODE_VALUES!r}")
