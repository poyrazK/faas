from typing import Literal

OperatorRuntimeConfigOperationStatus = Literal["blocked", "cancelled", "failed", "pending", "running", "succeeded"]

OPERATOR_RUNTIME_CONFIG_OPERATION_STATUS_VALUES: set[OperatorRuntimeConfigOperationStatus] = {
    "blocked",
    "cancelled",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_operator_runtime_config_operation_status(value: str) -> OperatorRuntimeConfigOperationStatus:
    if value in OPERATOR_RUNTIME_CONFIG_OPERATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_OPERATION_STATUS_VALUES!r}")
