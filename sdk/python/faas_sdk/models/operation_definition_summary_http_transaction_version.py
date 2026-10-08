from typing import Literal

OperationDefinitionSummaryHttpTransactionVersion = Literal[1]

OPERATION_DEFINITION_SUMMARY_HTTP_TRANSACTION_VERSION_VALUES: set[OperationDefinitionSummaryHttpTransactionVersion] = {
    1,
}


def check_operation_definition_summary_http_transaction_version(
    value: int,
) -> OperationDefinitionSummaryHttpTransactionVersion:
    if value in OPERATION_DEFINITION_SUMMARY_HTTP_TRANSACTION_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SUMMARY_HTTP_TRANSACTION_VERSION_VALUES!r}"
    )
