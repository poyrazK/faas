from typing import Literal

OperatorRuntimeConfigScope = Literal["control_plane", "daemon", "global", "node"]

OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES: set[OperatorRuntimeConfigScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_operator_runtime_config_scope(value: str) -> OperatorRuntimeConfigScope:
    if value in OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES!r}")
