from typing import Literal

DataRepairHealthItemPhase = Literal[
    "applying", "cleanup", "outcome_unknown", "publishing", "queued", "replacing", "resolving", "running"
]

DATA_REPAIR_HEALTH_ITEM_PHASE_VALUES: set[DataRepairHealthItemPhase] = {
    "applying",
    "cleanup",
    "outcome_unknown",
    "publishing",
    "queued",
    "replacing",
    "resolving",
    "running",
}


def check_data_repair_health_item_phase(value: str) -> DataRepairHealthItemPhase:
    if value in DATA_REPAIR_HEALTH_ITEM_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_REPAIR_HEALTH_ITEM_PHASE_VALUES!r}")
