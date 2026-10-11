from typing import Literal

OperationDefinitionSummaryRecovery = Literal["reconcile_on_unknown", "safe_retry"]

OPERATION_DEFINITION_SUMMARY_RECOVERY_VALUES: set[OperationDefinitionSummaryRecovery] = {
    "reconcile_on_unknown",
    "safe_retry",
}


def check_operation_definition_summary_recovery(value: str) -> OperationDefinitionSummaryRecovery:
    if value in OPERATION_DEFINITION_SUMMARY_RECOVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DEFINITION_SUMMARY_RECOVERY_VALUES!r}")
