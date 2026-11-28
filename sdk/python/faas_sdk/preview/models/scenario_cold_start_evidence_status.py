from typing import Literal

ScenarioColdStartEvidenceStatus = Literal["complete", "incomplete"]

SCENARIO_COLD_START_EVIDENCE_STATUS_VALUES: set[ScenarioColdStartEvidenceStatus] = {
    "complete",
    "incomplete",
}


def check_scenario_cold_start_evidence_status(value: str) -> ScenarioColdStartEvidenceStatus:
    if value in SCENARIO_COLD_START_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_COLD_START_EVIDENCE_STATUS_VALUES!r}")
