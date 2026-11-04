from typing import Literal

ScenarioPostgresFaultReceiptState = Literal["active", "cutting", "failed", "recovered", "recovering", "requested"]

SCENARIO_POSTGRES_FAULT_RECEIPT_STATE_VALUES: set[ScenarioPostgresFaultReceiptState] = {
    "active",
    "cutting",
    "failed",
    "recovered",
    "recovering",
    "requested",
}


def check_scenario_postgres_fault_receipt_state(value: str) -> ScenarioPostgresFaultReceiptState:
    if value in SCENARIO_POSTGRES_FAULT_RECEIPT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_POSTGRES_FAULT_RECEIPT_STATE_VALUES!r}")
