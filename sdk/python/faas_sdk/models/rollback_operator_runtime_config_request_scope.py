from typing import Literal

RollbackOperatorRuntimeConfigRequestScope = Literal["control_plane", "daemon", "global", "node"]

ROLLBACK_OPERATOR_RUNTIME_CONFIG_REQUEST_SCOPE_VALUES: set[RollbackOperatorRuntimeConfigRequestScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_rollback_operator_runtime_config_request_scope(value: str) -> RollbackOperatorRuntimeConfigRequestScope:
    if value in ROLLBACK_OPERATOR_RUNTIME_CONFIG_REQUEST_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROLLBACK_OPERATOR_RUNTIME_CONFIG_REQUEST_SCOPE_VALUES!r}"
    )
