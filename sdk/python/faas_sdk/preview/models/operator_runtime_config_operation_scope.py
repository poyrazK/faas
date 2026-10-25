from typing import Literal

OperatorRuntimeConfigOperationScope = Literal["control_plane", "daemon", "global", "node"]

OPERATOR_RUNTIME_CONFIG_OPERATION_SCOPE_VALUES: set[OperatorRuntimeConfigOperationScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_operator_runtime_config_operation_scope(value: str) -> OperatorRuntimeConfigOperationScope:
    if value in OPERATOR_RUNTIME_CONFIG_OPERATION_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_OPERATION_SCOPE_VALUES!r}")
