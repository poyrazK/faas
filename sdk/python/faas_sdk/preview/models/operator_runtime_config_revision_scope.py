from typing import Literal

OperatorRuntimeConfigRevisionScope = Literal["control_plane", "daemon", "global", "node"]

OPERATOR_RUNTIME_CONFIG_REVISION_SCOPE_VALUES: set[OperatorRuntimeConfigRevisionScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_operator_runtime_config_revision_scope(value: str) -> OperatorRuntimeConfigRevisionScope:
    if value in OPERATOR_RUNTIME_CONFIG_REVISION_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_REVISION_SCOPE_VALUES!r}")
