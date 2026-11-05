from typing import Literal

ScenarioSuspendResumeReceiptMethod = Literal["in_place"]

SCENARIO_SUSPEND_RESUME_RECEIPT_METHOD_VALUES: set[ScenarioSuspendResumeReceiptMethod] = {
    "in_place",
}


def check_scenario_suspend_resume_receipt_method(value: str) -> ScenarioSuspendResumeReceiptMethod:
    if value in SCENARIO_SUSPEND_RESUME_RECEIPT_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_RECEIPT_METHOD_VALUES!r}")
