from typing import Literal

ScenarioPostgresEvidenceStatus = Literal["complete"]

SCENARIO_POSTGRES_EVIDENCE_STATUS_VALUES: set[ScenarioPostgresEvidenceStatus] = {
    "complete",
}


def check_scenario_postgres_evidence_status(value: str) -> ScenarioPostgresEvidenceStatus:
    if value in SCENARIO_POSTGRES_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_EVIDENCE_STATUS_VALUES!r}")
