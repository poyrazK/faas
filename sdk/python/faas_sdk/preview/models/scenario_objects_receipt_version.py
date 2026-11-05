from typing import Literal

ScenarioObjectsReceiptVersion = Literal[1]

SCENARIO_OBJECTS_RECEIPT_VERSION_VALUES: set[ScenarioObjectsReceiptVersion] = {
    1,
}


def check_scenario_objects_receipt_version(value: int) -> ScenarioObjectsReceiptVersion:
    if value in SCENARIO_OBJECTS_RECEIPT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_RECEIPT_VERSION_VALUES!r}")
