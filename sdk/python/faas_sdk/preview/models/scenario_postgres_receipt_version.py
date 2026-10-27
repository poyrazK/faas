from typing import Literal

ScenarioPostgresReceiptVersion = Literal[1]

SCENARIO_POSTGRES_RECEIPT_VERSION_VALUES: set[ScenarioPostgresReceiptVersion] = {
    1,
}


def check_scenario_postgres_receipt_version(value: int) -> ScenarioPostgresReceiptVersion:
    if value in SCENARIO_POSTGRES_RECEIPT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_RECEIPT_VERSION_VALUES!r}")
