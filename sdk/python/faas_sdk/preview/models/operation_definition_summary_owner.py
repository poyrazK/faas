from typing import Literal

OperationDefinitionSummaryOwner = Literal["platform_tenant"]

OPERATION_DEFINITION_SUMMARY_OWNER_VALUES: set[OperationDefinitionSummaryOwner] = {
    "platform_tenant",
}


def check_operation_definition_summary_owner(value: str) -> OperationDefinitionSummaryOwner:
    if value in OPERATION_DEFINITION_SUMMARY_OWNER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SUMMARY_OWNER_VALUES!r}")
