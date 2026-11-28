from typing import Literal

OperationRecoveryInspectionExecutionKind = Literal["http", "job", "workflow"]

OPERATION_RECOVERY_INSPECTION_EXECUTION_KIND_VALUES: set[OperationRecoveryInspectionExecutionKind] = {
    "http",
    "job",
    "workflow",
}


def check_operation_recovery_inspection_execution_kind(value: str) -> OperationRecoveryInspectionExecutionKind:
    if value in OPERATION_RECOVERY_INSPECTION_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_RECOVERY_INSPECTION_EXECUTION_KIND_VALUES!r}"
    )
