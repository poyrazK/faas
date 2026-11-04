from typing import Literal

ScenarioColdStartEvidenceVersion = Literal[1]

SCENARIO_COLD_START_EVIDENCE_VERSION_VALUES: set[ScenarioColdStartEvidenceVersion] = {
    1,
}


def check_scenario_cold_start_evidence_version(value: int) -> ScenarioColdStartEvidenceVersion:
    if value in SCENARIO_COLD_START_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_COLD_START_EVIDENCE_VERSION_VALUES!r}")
