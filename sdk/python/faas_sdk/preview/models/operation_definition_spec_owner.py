from typing import Literal

OperationDefinitionSpecOwner = Literal["platform_tenant"]

OPERATION_DEFINITION_SPEC_OWNER_VALUES: set[OperationDefinitionSpecOwner] = {
    "platform_tenant",
}


def check_operation_definition_spec_owner(value: str) -> OperationDefinitionSpecOwner:
    if value in OPERATION_DEFINITION_SPEC_OWNER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SPEC_OWNER_VALUES!r}")
