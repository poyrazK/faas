from typing import Literal

UpdateOperatorRuntimeConfigBodyScope = Literal["control_plane", "daemon", "global", "node"]

UPDATE_OPERATOR_RUNTIME_CONFIG_BODY_SCOPE_VALUES: set[UpdateOperatorRuntimeConfigBodyScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_update_operator_runtime_config_body_scope(value: str) -> UpdateOperatorRuntimeConfigBodyScope:
    if value in UPDATE_OPERATOR_RUNTIME_CONFIG_BODY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_OPERATOR_RUNTIME_CONFIG_BODY_SCOPE_VALUES!r}")
