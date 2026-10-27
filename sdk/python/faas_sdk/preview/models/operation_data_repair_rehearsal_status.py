from typing import Literal

OperationDataRepairRehearsalStatus = Literal[
    "cancelled", "cleanup", "failed", "publishing", "queued", "running", "succeeded"
]

OPERATION_DATA_REPAIR_REHEARSAL_STATUS_VALUES: set[OperationDataRepairRehearsalStatus] = {
    "cancelled",
    "cleanup",
    "failed",
    "publishing",
    "queued",
    "running",
    "succeeded",
}


def check_operation_data_repair_rehearsal_status(value: str) -> OperationDataRepairRehearsalStatus:
    if value in OPERATION_DATA_REPAIR_REHEARSAL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DATA_REPAIR_REHEARSAL_STATUS_VALUES!r}")
