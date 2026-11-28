from typing import Literal

ScenarioSuspendResumeEvidenceStatus = Literal["complete", "incomplete", "pending"]

SCENARIO_SUSPEND_RESUME_EVIDENCE_STATUS_VALUES: set[ScenarioSuspendResumeEvidenceStatus] = {
    "complete",
    "incomplete",
    "pending",
}


def check_scenario_suspend_resume_evidence_status(value: str) -> ScenarioSuspendResumeEvidenceStatus:
    if value in SCENARIO_SUSPEND_RESUME_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_EVIDENCE_STATUS_VALUES!r}")
