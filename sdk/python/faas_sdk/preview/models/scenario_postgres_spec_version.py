from typing import Literal

ScenarioPostgresSpecVersion = Literal[1]

SCENARIO_POSTGRES_SPEC_VERSION_VALUES: set[ScenarioPostgresSpecVersion] = {
    1,
}


def check_scenario_postgres_spec_version(value: int) -> ScenarioPostgresSpecVersion:
    if value in SCENARIO_POSTGRES_SPEC_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_SPEC_VERSION_VALUES!r}")
