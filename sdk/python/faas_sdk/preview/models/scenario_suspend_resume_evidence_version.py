from typing import Literal

ScenarioSuspendResumeEvidenceVersion = Literal[1]

SCENARIO_SUSPEND_RESUME_EVIDENCE_VERSION_VALUES: set[ScenarioSuspendResumeEvidenceVersion] = {
    1,
}


def check_scenario_suspend_resume_evidence_version(value: int) -> ScenarioSuspendResumeEvidenceVersion:
    if value in SCENARIO_SUSPEND_RESUME_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_EVIDENCE_VERSION_VALUES!r}")
