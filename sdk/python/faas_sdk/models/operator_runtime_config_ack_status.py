from typing import Literal

OperatorRuntimeConfigAckStatus = Literal["applied", "failed"]

OPERATOR_RUNTIME_CONFIG_ACK_STATUS_VALUES: set[OperatorRuntimeConfigAckStatus] = {
    "applied",
    "failed",
}


def check_operator_runtime_config_ack_status(value: str) -> OperatorRuntimeConfigAckStatus:
    if value in OPERATOR_RUNTIME_CONFIG_ACK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_ACK_STATUS_VALUES!r}")
