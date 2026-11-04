from typing import Literal

OperatorRuntimeConfigApplyReceiptStatus = Literal["applied", "failed"]

OPERATOR_RUNTIME_CONFIG_APPLY_RECEIPT_STATUS_VALUES: set[OperatorRuntimeConfigApplyReceiptStatus] = {
    "applied",
    "failed",
}


def check_operator_runtime_config_apply_receipt_status(value: str) -> OperatorRuntimeConfigApplyReceiptStatus:
    if value in OPERATOR_RUNTIME_CONFIG_APPLY_RECEIPT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_APPLY_RECEIPT_STATUS_VALUES!r}"
    )
