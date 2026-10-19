from typing import Literal

ScenarioManagedWorkEvidenceStatus = Literal["complete", "incomplete", "pending"]

SCENARIO_MANAGED_WORK_EVIDENCE_STATUS_VALUES: set[ScenarioManagedWorkEvidenceStatus] = {
    "complete",
    "incomplete",
    "pending",
}


def check_scenario_managed_work_evidence_status(value: str) -> ScenarioManagedWorkEvidenceStatus:
    if value in SCENARIO_MANAGED_WORK_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_MANAGED_WORK_EVIDENCE_STATUS_VALUES!r}")
