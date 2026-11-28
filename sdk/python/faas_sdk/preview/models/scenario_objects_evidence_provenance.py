from typing import Literal

ScenarioObjectsEvidenceProvenance = Literal["platform_object_collector"]

SCENARIO_OBJECTS_EVIDENCE_PROVENANCE_VALUES: set[ScenarioObjectsEvidenceProvenance] = {
    "platform_object_collector",
}


def check_scenario_objects_evidence_provenance(value: str) -> ScenarioObjectsEvidenceProvenance:
    if value in SCENARIO_OBJECTS_EVIDENCE_PROVENANCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_EVIDENCE_PROVENANCE_VALUES!r}")
