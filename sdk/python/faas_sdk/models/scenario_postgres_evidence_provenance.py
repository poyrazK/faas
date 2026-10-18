from typing import Literal

ScenarioPostgresEvidenceProvenance = Literal["platform_postgres_collector"]

SCENARIO_POSTGRES_EVIDENCE_PROVENANCE_VALUES: set[ScenarioPostgresEvidenceProvenance] = {
    "platform_postgres_collector",
}


def check_scenario_postgres_evidence_provenance(value: str) -> ScenarioPostgresEvidenceProvenance:
    if value in SCENARIO_POSTGRES_EVIDENCE_PROVENANCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_EVIDENCE_PROVENANCE_VALUES!r}")
