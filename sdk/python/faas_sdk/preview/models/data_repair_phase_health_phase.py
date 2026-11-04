from typing import Literal

DataRepairPhaseHealthPhase = Literal[
    "applying", "cleanup", "outcome_unknown", "publishing", "queued", "replacing", "resolving", "running"
]

DATA_REPAIR_PHASE_HEALTH_PHASE_VALUES: set[DataRepairPhaseHealthPhase] = {
    "applying",
    "cleanup",
    "outcome_unknown",
    "publishing",
    "queued",
    "replacing",
    "resolving",
    "running",
}


def check_data_repair_phase_health_phase(value: str) -> DataRepairPhaseHealthPhase:
    if value in DATA_REPAIR_PHASE_HEALTH_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_REPAIR_PHASE_HEALTH_PHASE_VALUES!r}")
