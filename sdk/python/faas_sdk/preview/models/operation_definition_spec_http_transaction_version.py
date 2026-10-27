from typing import Literal

OperationDefinitionSpecHttpTransactionVersion = Literal[1]

OPERATION_DEFINITION_SPEC_HTTP_TRANSACTION_VERSION_VALUES: set[OperationDefinitionSpecHttpTransactionVersion] = {
    1,
}


def check_operation_definition_spec_http_transaction_version(
    value: int,
) -> OperationDefinitionSpecHttpTransactionVersion:
    if value in OPERATION_DEFINITION_SPEC_HTTP_TRANSACTION_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SPEC_HTTP_TRANSACTION_VERSION_VALUES!r}"
    )
