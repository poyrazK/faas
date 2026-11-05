from typing import Literal

OperationDefinitionSpecMethod = Literal["DELETE", "PATCH", "POST", "PUT"]

OPERATION_DEFINITION_SPEC_METHOD_VALUES: set[OperationDefinitionSpecMethod] = {
    "DELETE",
    "PATCH",
    "POST",
    "PUT",
}


def check_operation_definition_spec_method(value: str) -> OperationDefinitionSpecMethod:
    if value in OPERATION_DEFINITION_SPEC_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SPEC_METHOD_VALUES!r}")
