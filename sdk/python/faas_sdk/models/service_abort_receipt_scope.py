from typing import Literal

ServiceAbortReceiptScope = Literal["default"]

SERVICE_ABORT_RECEIPT_SCOPE_VALUES: set[ServiceAbortReceiptScope] = {
    "default",
}


def check_service_abort_receipt_scope(value: str) -> ServiceAbortReceiptScope:
    if value in SERVICE_ABORT_RECEIPT_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ABORT_RECEIPT_SCOPE_VALUES!r}")
