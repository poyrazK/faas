from typing import Literal

OperationDefinitionSpecRecovery = Literal["reconcile_on_unknown", "safe_retry"]

OPERATION_DEFINITION_SPEC_RECOVERY_VALUES: set[OperationDefinitionSpecRecovery] = {
    "reconcile_on_unknown",
    "safe_retry",
}


def check_operation_definition_spec_recovery(value: str) -> OperationDefinitionSpecRecovery:
    if value in OPERATION_DEFINITION_SPEC_RECOVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SPEC_RECOVERY_VALUES!r}")
