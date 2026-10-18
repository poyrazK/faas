from typing import Literal

ScenarioManagedWorkParticipantObjects = Literal["read", "read_write"]

SCENARIO_MANAGED_WORK_PARTICIPANT_OBJECTS_VALUES: set[ScenarioManagedWorkParticipantObjects] = {
    "read",
    "read_write",
}


def check_scenario_managed_work_participant_objects(value: str) -> ScenarioManagedWorkParticipantObjects:
    if value in SCENARIO_MANAGED_WORK_PARTICIPANT_OBJECTS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_MANAGED_WORK_PARTICIPANT_OBJECTS_VALUES!r}")
