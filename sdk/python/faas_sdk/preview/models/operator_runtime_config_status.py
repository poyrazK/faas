from typing import Literal

OperatorRuntimeConfigStatus = Literal["applied", "blocked", "failed", "pending"]

OPERATOR_RUNTIME_CONFIG_STATUS_VALUES: set[OperatorRuntimeConfigStatus] = {
    "applied",
    "blocked",
    "failed",
    "pending",
}


def check_operator_runtime_config_status(value: str) -> OperatorRuntimeConfigStatus:
    if value in OPERATOR_RUNTIME_CONFIG_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_STATUS_VALUES!r}")
