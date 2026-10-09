from typing import Literal

OperationDefinitionSummaryTransactionReceipt = Literal["postgres_v1"]

OPERATION_DEFINITION_SUMMARY_TRANSACTION_RECEIPT_VALUES: set[OperationDefinitionSummaryTransactionReceipt] = {
    "postgres_v1",
}


def check_operation_definition_summary_transaction_receipt(value: str) -> OperationDefinitionSummaryTransactionReceipt:
    if value in OPERATION_DEFINITION_SUMMARY_TRANSACTION_RECEIPT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SUMMARY_TRANSACTION_RECEIPT_VALUES!r}"
    )
