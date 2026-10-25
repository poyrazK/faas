from typing import Literal

ScenarioManagedWorkParticipantPostgres = Literal["read_write"]

SCENARIO_MANAGED_WORK_PARTICIPANT_POSTGRES_VALUES: set[ScenarioManagedWorkParticipantPostgres] = {
    "read_write",
}


def check_scenario_managed_work_participant_postgres(value: str) -> ScenarioManagedWorkParticipantPostgres:
    if value in SCENARIO_MANAGED_WORK_PARTICIPANT_POSTGRES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_MANAGED_WORK_PARTICIPANT_POSTGRES_VALUES!r}"
    )
