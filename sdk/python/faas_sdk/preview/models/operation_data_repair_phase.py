from typing import Literal

OperationDataRepairPhase = Literal[
    "applying",
    "approved",
    "awaiting_approval",
    "closed",
    "failed",
    "outcome_unknown",
    "prepared",
    "rehearsing",
    "rejected",
    "replaced",
    "replacing",
    "resolving",
    "succeeded",
]

OPERATION_DATA_REPAIR_PHASE_VALUES: set[OperationDataRepairPhase] = {
    "applying",
    "approved",
    "awaiting_approval",
    "closed",
    "failed",
    "outcome_unknown",
    "prepared",
    "rehearsing",
    "rejected",
    "replaced",
    "replacing",
    "resolving",
    "succeeded",
}


def check_operation_data_repair_phase(value: str) -> OperationDataRepairPhase:
    if value in OPERATION_DATA_REPAIR_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DATA_REPAIR_PHASE_VALUES!r}")
