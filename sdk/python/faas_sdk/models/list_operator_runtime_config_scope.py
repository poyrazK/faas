from typing import Literal

ListOperatorRuntimeConfigScope = Literal["control_plane", "daemon", "global", "node"]

LIST_OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES: set[ListOperatorRuntimeConfigScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_list_operator_runtime_config_scope(value: str) -> ListOperatorRuntimeConfigScope:
    if value in LIST_OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_OPERATOR_RUNTIME_CONFIG_SCOPE_VALUES!r}")
