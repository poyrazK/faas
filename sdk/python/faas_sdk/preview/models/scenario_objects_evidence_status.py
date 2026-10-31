from typing import Literal

ScenarioObjectsEvidenceStatus = Literal["complete"]

SCENARIO_OBJECTS_EVIDENCE_STATUS_VALUES: set[ScenarioObjectsEvidenceStatus] = {
    "complete",
}


def check_scenario_objects_evidence_status(value: str) -> ScenarioObjectsEvidenceStatus:
    if value in SCENARIO_OBJECTS_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_EVIDENCE_STATUS_VALUES!r}")
