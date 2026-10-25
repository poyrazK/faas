from typing import Literal

ScenarioManagedWorkEvidenceVersion = Literal[1]

SCENARIO_MANAGED_WORK_EVIDENCE_VERSION_VALUES: set[ScenarioManagedWorkEvidenceVersion] = {
    1,
}


def check_scenario_managed_work_evidence_version(value: int) -> ScenarioManagedWorkEvidenceVersion:
    if value in SCENARIO_MANAGED_WORK_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_MANAGED_WORK_EVIDENCE_VERSION_VALUES!r}")
