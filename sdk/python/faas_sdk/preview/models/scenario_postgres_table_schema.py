from typing import Literal

ScenarioPostgresTableSchema = Literal["public"]

SCENARIO_POSTGRES_TABLE_SCHEMA_VALUES: set[ScenarioPostgresTableSchema] = {
    "public",
}


def check_scenario_postgres_table_schema(value: str) -> ScenarioPostgresTableSchema:
    if value in SCENARIO_POSTGRES_TABLE_SCHEMA_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_TABLE_SCHEMA_VALUES!r}")
