from typing import Literal

OperationDefinitionSummaryMethod = Literal["DELETE", "PATCH", "POST", "PUT"]

OPERATION_DEFINITION_SUMMARY_METHOD_VALUES: set[OperationDefinitionSummaryMethod] = {
    "DELETE",
    "PATCH",
    "POST",
    "PUT",
}


def check_operation_definition_summary_method(value: str) -> OperationDefinitionSummaryMethod:
    if value in OPERATION_DEFINITION_SUMMARY_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SUMMARY_METHOD_VALUES!r}")
