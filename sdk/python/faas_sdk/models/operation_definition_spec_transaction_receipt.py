from typing import Literal

OperationDefinitionSpecTransactionReceipt = Literal["postgres_v1"]

OPERATION_DEFINITION_SPEC_TRANSACTION_RECEIPT_VALUES: set[OperationDefinitionSpecTransactionReceipt] = {
    "postgres_v1",
}


def check_operation_definition_spec_transaction_receipt(value: str) -> OperationDefinitionSpecTransactionReceipt:
    if value in OPERATION_DEFINITION_SPEC_TRANSACTION_RECEIPT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SPEC_TRANSACTION_RECEIPT_VALUES!r}"
    )
