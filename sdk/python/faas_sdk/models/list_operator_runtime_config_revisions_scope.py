from typing import Literal

ListOperatorRuntimeConfigRevisionsScope = Literal["control_plane", "daemon", "global", "node"]

LIST_OPERATOR_RUNTIME_CONFIG_REVISIONS_SCOPE_VALUES: set[ListOperatorRuntimeConfigRevisionsScope] = {
    "control_plane",
    "daemon",
    "global",
    "node",
}


def check_list_operator_runtime_config_revisions_scope(value: str) -> ListOperatorRuntimeConfigRevisionsScope:
    if value in LIST_OPERATOR_RUNTIME_CONFIG_REVISIONS_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_OPERATOR_RUNTIME_CONFIG_REVISIONS_SCOPE_VALUES!r}"
    )
